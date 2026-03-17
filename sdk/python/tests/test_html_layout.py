from __future__ import annotations

import os
import sys
import tempfile
from pathlib import Path
from typing import Callable

import pytest


REPO_ROOT = Path(__file__).resolve().parents[3]
SDK_ROOT = REPO_ROOT / "sdk" / "python"
if str(SDK_ROOT) not in sys.path:
    sys.path.insert(0, str(SDK_ROOT))

from primitivebox.html_layout import HTMLLayoutServer  # noqa: E402


class StubClient:
    def call(self, method: str, params: dict, headers: dict | None = None) -> dict:
        return {"method": method, "params": params, "headers": headers}


@pytest.fixture
def server() -> HTMLLayoutServer:
    return HTMLLayoutServer(StubClient())


@pytest.fixture
def temp_html() -> Callable[[str], str]:
    created_paths: list[str] = []

    def _create(content: str) -> str:
        handle = tempfile.NamedTemporaryFile("w", suffix=".html", delete=False, encoding="utf-8")
        handle.write(content)
        handle.close()
        created_paths.append(handle.name)
        return handle.name

    yield _create

    for path in created_paths:
        if os.path.exists(path):
            os.unlink(path)


def test_style_read_basic(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html('<html><body><p id="intro" style="font-size:16px;color:#333">Hello</p></body></html>')

    result = server.style_read(path, "#intro")

    assert result["count"] == 1
    assert result["matches"][0]["styles"]["font-size"] == "16px"


def test_style_read_no_match(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html("<html><body><p>Hi</p></body></html>")

    result = server.style_read(path, ".nonexistent")

    assert result == {"matches": [], "count": 0}


def test_style_apply_modifies_file(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html('<html><body><p class="lead">One</p><p class="lead">Two</p></body></html>')

    result = server.style_apply(path, "p.lead", {"font-size": "18px"})

    assert result["modified_count"] == 2
    html = Path(path).read_text(encoding="utf-8")
    assert html.count("font-size: 18px") == 2


def test_style_apply_invalid_font_size(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html("<html><body><p>Hello</p></body></html>")
    original = Path(path).read_text(encoding="utf-8")

    with pytest.raises(ValueError):
        server.style_apply(path, "p", {"font-size": "abc"})

    assert Path(path).read_text(encoding="utf-8") == original


def test_style_apply_no_match(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html("<html><body><p>Hello</p></body></html>")
    original = Path(path).read_text(encoding="utf-8")

    result = server.style_apply(path, ".ghost", {"font-size": "18px"})

    assert result["modified_count"] == 0
    assert Path(path).read_text(encoding="utf-8") == original


def test_apply_tokens_replaces_in_style_tag(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html(
        "<html><head><style>body { color: var(--primary-color); }</style></head><body></body></html>"
    )

    result = server.style_apply_tokens(path, {"--primary-color": "#2563eb"})

    assert result["replaced_count"] == 1
    html = Path(path).read_text(encoding="utf-8")
    assert "var(--primary-color)" not in html
    assert "#2563eb" in html


def test_apply_tokens_not_found(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html("<html><body><p>No tokens here</p></body></html>")

    result = server.style_apply_tokens(path, {"--unused": "#fff"})

    assert result["replaced_count"] == 0
    assert result["tokens_not_found"] == ["--unused"]


def test_contrast_passes_aa(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html(
        '<html><body><p style="color:#000000;background-color:#ffffff">Readable</p></body></html>'
    )

    result = server.verify_contrast(path, "p", level="AA")

    assert result["passed"] is True
    assert result["results"][0]["ratio"] >= 4.5


def test_contrast_fails_aa(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html(
        '<html><body><p style="color:#aaaaaa;background-color:#ffffff">Low contrast</p></body></html>'
    )

    result = server.verify_contrast(path, "p", level="AA")

    assert result["passed"] is False
    assert result["results"][0]["passed"] is False


def test_contrast_skip_no_inline_color(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html("<html><body><p>No inline style</p></body></html>")

    result = server.verify_contrast(path, "p", level="AA")

    assert result["passed"] is True
    assert result["results"] == []
    assert "skipped" in result["summary"].lower()


def test_structure_passes(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html(
        """
        <html>
          <head>
            <meta charset="utf-8" />
            <meta name="viewport" content="width=device-width, initial-scale=1" />
          </head>
          <body>
            <h1>Title</h1>
            <h2>Section</h2>
            <img src="x.png" alt="Example" />
            <a href="/docs">Docs</a>
          </body>
        </html>
        """
    )

    result = server.verify_structure(path)

    assert result["passed"] is True
    assert result["issues"] == []


def test_structure_missing_alt(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    missing_alt_path = temp_html(
        """
        <html>
          <head><meta charset="utf-8" /></head>
          <body><h1>Title</h1><img src="x.png" /></body>
        </html>
        """
    )
    missing_h1_path = temp_html(
        """
        <html>
          <head>
            <meta charset="utf-8" />
            <meta name="viewport" content="width=device-width, initial-scale=1" />
          </head>
          <body><h2>Section</h2></body>
        </html>
        """
    )
    duplicate_h1_path = temp_html(
        """
        <html>
          <head>
            <meta charset="utf-8" />
            <meta name="viewport" content="width=device-width, initial-scale=1" />
          </head>
          <body><h1>One</h1><h1>Two</h1></body>
        </html>
        """
    )

    missing_alt = server.verify_structure(missing_alt_path)
    missing_h1 = server.verify_structure(missing_h1_path)
    duplicate_h1 = server.verify_structure(duplicate_h1_path)

    assert missing_alt["passed"] is False
    assert any(issue["severity"] == "error" and issue["check"] == "img_alt" for issue in missing_alt["issues"])
    assert missing_h1["passed"] is False
    assert duplicate_h1["passed"] is False
    assert any(issue["severity"] == "error" and issue["check"] == "heading_hierarchy" for issue in missing_h1["issues"])
    assert any(issue["severity"] == "error" and issue["check"] == "heading_hierarchy" for issue in duplicate_h1["issues"])


def test_structure_heading_skip(server: HTMLLayoutServer, temp_html: Callable[[str], str]) -> None:
    path = temp_html(
        """
        <html>
          <head>
            <meta charset="utf-8" />
            <meta name="viewport" content="width=device-width, initial-scale=1" />
          </head>
          <body>
            <h1>Title</h1>
            <h3>Subsection</h3>
          </body>
        </html>
        """
    )

    result = server.verify_structure(path)

    assert result["passed"] is True
    assert any(
        issue["check"] == "heading_hierarchy" and issue["severity"] == "warning"
        for issue in result["issues"]
    )

