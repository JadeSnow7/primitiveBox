"""
HTML layout app primitives built on top of PrimitiveBox AppServer.
"""

from __future__ import annotations

import os
import re
from typing import Any, Optional

import tinycss2
from bs4 import BeautifulSoup, Tag


_HEX_SHORT_RE = re.compile(r"^#([0-9a-fA-F]{3})$")
_HEX_LONG_RE = re.compile(r"^#([0-9a-fA-F]{6})$")
_RGB_RE = re.compile(r"^rgba?\(([^)]+)\)$", re.IGNORECASE)
_FONT_SIZE_RE = re.compile(r"^(?P<value>\d+(?:\.\d+)?)(?P<unit>px|em|rem)$", re.IGNORECASE)
_NUMERIC_RE = re.compile(r"^\d+(?:\.\d+)?$")
_SELECTOR_RE = re.compile(
    r"^(?:(?P<tag>[a-zA-Z][a-zA-Z0-9]*)|)"
    r"(?:(?P<class>\.[A-Za-z_][\w-]*)|(?P<id>\#[A-Za-z_][\w-]*))?$"
)


def _read_html(path: str) -> str:
    if not os.path.exists(path):
        raise ValueError(f"HTML file not found: {path}")
    with open(path, "r", encoding="utf-8") as handle:
        return handle.read()


def _write_html(path: str, html: str) -> None:
    with open(path, "w", encoding="utf-8") as handle:
        handle.write(html)


def _parse_color(color_str: str) -> Optional[tuple[int, int, int]]:
    """
    Parse a CSS color string into an (R, G, B) tuple.
    """
    value = color_str.strip()
    match = _HEX_SHORT_RE.fullmatch(value)
    if match:
        digits = match.group(1)
        return tuple(int(ch * 2, 16) for ch in digits)

    match = _HEX_LONG_RE.fullmatch(value)
    if match:
        digits = match.group(1)
        return tuple(int(digits[index:index + 2], 16) for index in (0, 2, 4))

    match = _RGB_RE.fullmatch(value)
    if not match:
        return None

    parts = [part.strip() for part in match.group(1).split(",")]
    if len(parts) not in (3, 4):
        return None

    rgb: list[int] = []
    for part in parts[:3]:
        if not _NUMERIC_RE.fullmatch(part):
            return None
        channel = float(part)
        if channel < 0 or channel > 255 or not channel.is_integer():
            return None
        rgb.append(int(channel))
    return tuple(rgb)


def _srgb_to_linear(channel: int) -> float:
    value = channel / 255.0
    if value <= 0.04045:
        return value / 12.92
    return ((value + 0.055) / 1.055) ** 2.4


def _relative_luminance(r: int, g: int, b: int) -> float:
    """
    Compute relative luminance using the WCAG 2.1 formula.
    """
    return (
        0.2126 * _srgb_to_linear(r)
        + 0.7152 * _srgb_to_linear(g)
        + 0.0722 * _srgb_to_linear(b)
    )


def _contrast_ratio(color1: str, color2: str) -> Optional[float]:
    """
    Compute the WCAG contrast ratio between two CSS colors.
    """
    parsed1 = _parse_color(color1)
    parsed2 = _parse_color(color2)
    if parsed1 is None or parsed2 is None:
        return None
    luminance1 = _relative_luminance(*parsed1)
    luminance2 = _relative_luminance(*parsed2)
    lighter = max(luminance1, luminance2)
    darker = min(luminance1, luminance2)
    return (lighter + 0.05) / (darker + 0.05)


def _css_selector_matches(tag: Tag, selector: str) -> bool:
    """
    Match a limited CSS selector against a BeautifulSoup tag.
    """
    selector = selector.strip()
    match = _SELECTOR_RE.fullmatch(selector)
    if not match:
        return False

    tag_name = match.group("tag")
    class_name = match.group("class")
    id_name = match.group("id")

    if tag_name and tag.name != tag_name.lower():
        return False
    if class_name:
        classes = tag.get("class", [])
        return class_name[1:] in classes
    if id_name:
        return tag.get("id") == id_name[1:]
    return True


def _parse_inline_styles(style_attr: str) -> dict[str, str]:
    styles: dict[str, str] = {}
    for node in tinycss2.parse_declaration_list(style_attr, skip_whitespace=True, skip_comments=True):
        if getattr(node, "type", "") != "declaration":
            continue
        serialized = tinycss2.serialize(node.value).strip()
        styles[node.name.lower()] = serialized
    return styles


def _serialize_inline_styles(style_map: dict[str, str]) -> str:
    return "; ".join(f"{name}: {value}" for name, value in style_map.items())


def _apply_inline_style(tag: Tag, styles: dict[str, str]) -> None:
    """
    Merge CSS properties into the tag's inline style attribute.
    """
    merged = _parse_inline_styles(tag.get("style", ""))
    merged.update({key.lower(): value for key, value in styles.items()})
    if merged:
        tag["style"] = _serialize_inline_styles(merged)
    elif tag.has_attr("style"):
        del tag["style"]


def _find_matching_tags(soup: BeautifulSoup, selector: str) -> list[Tag]:
    return [tag for tag in soup.find_all(True) if _css_selector_matches(tag, selector)]


def _extract_var_name(arguments: list[Any]) -> Optional[str]:
    filtered = [
        token for token in arguments
        if getattr(token, "type", "") not in {"whitespace", "comment"}
    ]
    if len(filtered) != 1:
        return None
    token = filtered[0]
    if getattr(token, "type", "") != "ident":
        return None
    value = getattr(token, "value", "")
    if not value.startswith("--"):
        return None
    return value


def _replace_component_values(
    tokens: list[Any],
    token_map: dict[str, str],
    used_tokens: set[str],
) -> tuple[list[Any], int]:
    replaced: list[Any] = []
    count = 0
    for token in tokens:
        token_type = getattr(token, "type", "")
        if token_type == "function" and getattr(token, "lower_name", "") == "var":
            token_name = _extract_var_name(getattr(token, "arguments", []))
            if token_name and token_name in token_map:
                replacement = tinycss2.parse_component_value_list(token_map[token_name])
                replaced.extend(replacement)
                used_tokens.add(token_name)
                count += 1
                continue

        if token_type in {"function", "() block", "[] block", "{} block"} and hasattr(token, "content"):
            token.content, nested = _replace_component_values(token.content, token_map, used_tokens)
            count += nested
        elif token_type == "function" and hasattr(token, "arguments"):
            token.arguments, nested = _replace_component_values(token.arguments, token_map, used_tokens)
            count += nested
        elif token_type == "declaration" and hasattr(token, "value"):
            token.value, nested = _replace_component_values(token.value, token_map, used_tokens)
            count += nested
        elif token_type in {"qualified-rule", "at-rule"}:
            if hasattr(token, "prelude") and token.prelude is not None:
                token.prelude, nested = _replace_component_values(token.prelude, token_map, used_tokens)
                count += nested
            if hasattr(token, "content") and token.content is not None:
                token.content, nested = _replace_component_values(token.content, token_map, used_tokens)
                count += nested

        replaced.append(token)
    return replaced, count


def _replace_tokens_in_css(css_text: str, token_map: dict[str, str], used_tokens: set[str]) -> tuple[str, int]:
    rules = tinycss2.parse_stylesheet(css_text, skip_whitespace=False, skip_comments=False)
    updated_rules, count = _replace_component_values(rules, token_map, used_tokens)
    return tinycss2.serialize(updated_rules), count


def _replace_tokens_in_attribute(value: str, token_map: dict[str, str], used_tokens: set[str]) -> tuple[str, int]:
    tokens = tinycss2.parse_component_value_list(value)
    updated_tokens, count = _replace_component_values(tokens, token_map, used_tokens)
    return tinycss2.serialize(updated_tokens), count


def _validate_styles(styles: dict[str, str]) -> dict[str, str]:
    validated: dict[str, str] = {}
    for raw_name, raw_value in styles.items():
        if not isinstance(raw_name, str) or not isinstance(raw_value, str):
            raise ValueError("styles keys and values must be strings")
        name = raw_name.strip().lower()
        value = raw_value.strip()
        if not name or not value:
            raise ValueError("styles keys and values must be non-empty")
        if name == "font-size":
            match = _FONT_SIZE_RE.fullmatch(value)
            if not match or float(match.group("value")) <= 0:
                raise ValueError("font-size must be a positive px, em, or rem value")
        elif name in {"color", "background-color"}:
            if _parse_color(value) is None:
                raise ValueError(f"{name} must be a parseable CSS color")
        elif name == "line-height":
            if value.lower() != "normal":
                if not _NUMERIC_RE.fullmatch(value) or float(value) <= 0:
                    raise ValueError("line-height must be numeric or normal")
        validated[name] = value
    return validated


def _font_size_px(value: Optional[str]) -> Optional[float]:
    if not value:
        return None
    match = _FONT_SIZE_RE.fullmatch(value.strip())
    if not match:
        return None
    size = float(match.group("value"))
    unit = match.group("unit").lower()
    if unit == "px":
        return size
    if unit in {"em", "rem"}:
        return size * 16.0
    return None


def _is_large_text(styles: dict[str, str]) -> bool:
    size_px = _font_size_px(styles.get("font-size"))
    font_weight = styles.get("font-weight", "").strip().lower()
    is_bold = font_weight == "bold"
    if not is_bold:
        try:
            is_bold = float(font_weight) >= 700
        except ValueError:
            is_bold = False
    if size_px is None:
        return False
    if size_px >= 18.0:
        return True
    return is_bold and size_px >= 14.0


def _required_contrast(level: str, large_text: bool) -> float:
    upper = level.upper()
    if upper not in {"AA", "AAA"}:
        raise ValueError("level must be AA or AAA")
    if upper == "AA":
        return 3.0 if large_text else 4.5
    return 4.5 if large_text else 7.0


def _selector_identity(tag: Tag) -> str:
    parts = [tag.name]
    if tag.get("id"):
        parts.append(f"#{tag['id']}")
    elif tag.get("class"):
        parts.extend(f".{class_name}" for class_name in tag.get("class", []))
    return "".join(parts)


def _issue(check: str, severity: str, message: str) -> dict[str, str]:
    return {"check": check, "severity": severity, "message": message}


class HTMLLayoutServer:
    """
    HTML layout primitive collection exposed through AppServer.
    """

    SOCKET_PATH = "/tmp/html_layout.sock"

    def __init__(self, client):
        from primitivebox.app import AppServer

        self._server = AppServer("html", client)
        self._register_all()

    def _register_all(self) -> None:
        self._server.primitive(
            "style.read",
            socket_path=self.SOCKET_PATH,
            description="Read inline styles from matching HTML elements.",
            input_schema={
                "type": "object",
                "required": ["path", "selector"],
                "properties": {
                    "path": {"type": "string"},
                    "selector": {"type": "string"},
                },
            },
            output_schema={
                "type": "object",
                "required": ["matches", "count"],
                "properties": {
                    "matches": {"type": "array"},
                    "count": {"type": "integer"},
                },
            },
            category="query",
            reversible=True,
            risk_level="low",
        )(self.style_read)

        self._server.primitive(
            "style.apply",
            socket_path=self.SOCKET_PATH,
            description="Apply inline styles to all matching HTML elements.",
            input_schema={
                "type": "object",
                "required": ["path", "selector", "styles"],
                "properties": {
                    "path": {"type": "string"},
                    "selector": {"type": "string"},
                    "styles": {"type": "object"},
                },
            },
            output_schema={
                "type": "object",
                "required": ["modified_count", "selector", "applied_styles"],
                "properties": {
                    "modified_count": {"type": "integer"},
                    "selector": {"type": "string"},
                    "applied_styles": {"type": "object"},
                },
            },
            category="mutation",
            reversible=True,
            risk_level="low",
        )(self.style_apply)

        self._server.primitive(
            "style.apply_tokens",
            socket_path=self.SOCKET_PATH,
            description="Replace CSS variable references in HTML and style tags.",
            input_schema={
                "type": "object",
                "required": ["path", "token_map"],
                "properties": {
                    "path": {"type": "string"},
                    "token_map": {"type": "object"},
                },
            },
            output_schema={
                "type": "object",
                "required": ["replaced_count", "tokens_applied", "tokens_not_found"],
                "properties": {
                    "replaced_count": {"type": "integer"},
                    "tokens_applied": {"type": "array"},
                    "tokens_not_found": {"type": "array"},
                },
            },
            category="mutation",
            reversible=True,
            risk_level="medium",
        )(self.style_apply_tokens)

        self._server.primitive(
            "verify.contrast",
            socket_path=self.SOCKET_PATH,
            description="Check inline foreground/background contrast against WCAG.",
            input_schema={
                "type": "object",
                "required": ["path", "selector"],
                "properties": {
                    "path": {"type": "string"},
                    "selector": {"type": "string"},
                    "level": {"type": "string", "enum": ["AA", "AAA"]},
                },
            },
            output_schema={
                "type": "object",
                "required": ["passed", "level", "results", "summary"],
                "properties": {
                    "passed": {"type": "boolean"},
                    "level": {"type": "string"},
                    "results": {"type": "array"},
                    "summary": {"type": "string"},
                },
            },
            category="verification",
            reversible=True,
            risk_level="low",
        )(self.verify_contrast)

        self._server.primitive(
            "verify.structure",
            socket_path=self.SOCKET_PATH,
            description="Validate HTML heading, image, meta, and link structure.",
            input_schema={
                "type": "object",
                "required": ["path"],
                "properties": {
                    "path": {"type": "string"},
                },
            },
            output_schema={
                "type": "object",
                "required": ["passed", "issues", "summary"],
                "properties": {
                    "passed": {"type": "boolean"},
                    "issues": {"type": "array"},
                    "summary": {"type": "string"},
                },
            },
            category="verification",
            reversible=True,
            risk_level="low",
        )(self.verify_structure)

    def serve(self) -> None:
        self._server.serve(self.SOCKET_PATH)

    def style_read(self, path: str, selector: str) -> dict[str, Any]:
        html = _read_html(path)
        soup = BeautifulSoup(html, "html.parser")
        matches = []
        for tag in _find_matching_tags(soup, selector):
            matches.append(
                {
                    "tag": tag.name,
                    "id": tag.get("id"),
                    "classes": list(tag.get("class", [])),
                    "styles": _parse_inline_styles(tag.get("style", "")),
                }
            )
        return {"matches": matches, "count": len(matches)}

    def style_apply(self, path: str, selector: str, styles: dict[str, str]) -> dict[str, Any]:
        validated = _validate_styles(styles)
        html = _read_html(path)
        soup = BeautifulSoup(html, "html.parser")
        modified_count = 0
        for tag in _find_matching_tags(soup, selector):
            before = tag.get("style", "")
            _apply_inline_style(tag, validated)
            if tag.get("style", "") != before:
                modified_count += 1

        if modified_count > 0:
            _write_html(path, str(soup))

        return {
            "modified_count": modified_count,
            "selector": selector,
            "applied_styles": validated,
        }

    def style_apply_tokens(self, path: str, token_map: dict[str, str]) -> dict[str, Any]:
        if not all(isinstance(key, str) and isinstance(value, str) for key, value in token_map.items()):
            raise ValueError("token_map keys and values must be strings")
        html = _read_html(path)
        soup = BeautifulSoup(html, "html.parser")
        used_tokens: set[str] = set()
        replaced_count = 0

        for style_tag in soup.find_all("style"):
            css_text = style_tag.string if style_tag.string is not None else style_tag.get_text()
            updated_css, replacements = _replace_tokens_in_css(css_text, token_map, used_tokens)
            if replacements > 0:
                style_tag.clear()
                style_tag.append(updated_css)
                replaced_count += replacements

        for tag in soup.find_all(True):
            for attr_name, attr_value in list(tag.attrs.items()):
                if not isinstance(attr_value, str) or "var(" not in attr_value:
                    continue
                updated_value, replacements = _replace_tokens_in_attribute(attr_value, token_map, used_tokens)
                if replacements > 0:
                    tag[attr_name] = updated_value
                    replaced_count += replacements

        if replaced_count > 0:
            _write_html(path, str(soup))

        tokens_applied = sorted(used_tokens)
        tokens_not_found = sorted(token for token in token_map if token not in used_tokens)
        return {
            "replaced_count": replaced_count,
            "tokens_applied": tokens_applied,
            "tokens_not_found": tokens_not_found,
        }

    def verify_contrast(self, path: str, selector: str, level: str = "AA") -> dict[str, Any]:
        html = _read_html(path)
        soup = BeautifulSoup(html, "html.parser")
        level = level.upper()
        _required_contrast(level, False)

        results = []
        skipped = 0
        for tag in _find_matching_tags(soup, selector):
            styles = _parse_inline_styles(tag.get("style", ""))
            foreground = styles.get("color")
            background = styles.get("background-color")
            if not foreground or not background:
                skipped += 1
                continue

            ratio = _contrast_ratio(foreground, background)
            if ratio is None:
                skipped += 1
                continue

            required = _required_contrast(level, _is_large_text(styles))
            passed = ratio >= required
            results.append(
                {
                    "selector_match": _selector_identity(tag),
                    "foreground": foreground,
                    "background": background,
                    "ratio": round(ratio, 2),
                    "required": required,
                    "passed": passed,
                }
            )

        if not results:
            return {
                "passed": True,
                "level": level,
                "results": [],
                "summary": f"All {skipped} matched elements were skipped because inline color/background values were unavailable or unparsable.",
            }

        passed_count = sum(1 for result in results if result["passed"])
        return {
            "passed": passed_count == len(results),
            "level": level,
            "results": results,
            "summary": f"{passed_count}/{len(results)} elements passed {level} contrast check",
        }

    def verify_structure(self, path: str) -> dict[str, Any]:
        html = _read_html(path)
        soup = BeautifulSoup(html, "html.parser")
        issues: list[dict[str, str]] = []

        h1_tags = soup.find_all("h1")
        if not h1_tags:
            issues.append(_issue("heading_hierarchy", "error", "Document must contain exactly one h1 heading; none found."))
        elif len(h1_tags) > 1:
            issues.append(_issue("heading_hierarchy", "error", f"Document must contain exactly one h1 heading; found {len(h1_tags)}."))

        previous_level: Optional[int] = None
        for tag in soup.find_all(["h1", "h2", "h3", "h4", "h5", "h6"]):
            current_level = int(tag.name[1])
            if previous_level is not None and current_level > previous_level + 1:
                issues.append(
                    _issue(
                        "heading_hierarchy",
                        "warning",
                        f"Heading level skipped: h{previous_level} -> h{current_level}.",
                    )
                )
            previous_level = current_level

        for image in soup.find_all("img"):
            alt = image.get("alt")
            if alt is None or not str(alt).strip():
                source = image.get("src", "")
                issues.append(_issue("img_alt", "error", f"Image is missing a non-empty alt attribute: {source}"))

        head = soup.head
        has_charset = False
        has_viewport = False
        if head is not None:
            for meta in head.find_all("meta"):
                if meta.get("charset"):
                    has_charset = True
                if str(meta.get("name", "")).strip().lower() == "viewport":
                    has_viewport = True

        if not has_charset:
            issues.append(_issue("meta_charset", "error", "Head is missing a meta charset declaration."))
        if not has_viewport:
            issues.append(_issue("meta_viewport", "warning", "Head is missing a meta viewport declaration."))

        for link in soup.find_all("a"):
            href = str(link.get("href", "")).strip()
            if not href or href == "#":
                issues.append(_issue("link_href", "warning", "Anchor contains an empty or placeholder href value."))

        error_count = sum(1 for issue in issues if issue["severity"] == "error")
        warning_count = sum(1 for issue in issues if issue["severity"] == "warning")
        return {
            "passed": error_count == 0,
            "issues": issues,
            "summary": f"{error_count} errors, {warning_count} warnings",
        }
