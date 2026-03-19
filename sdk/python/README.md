# PrimitiveBox Python SDK

Python client library for PrimitiveBox, the checkpointed sandbox runtime for AI
agents.

## Install

```bash
pip install primitivebox
```

For local development:

```bash
pip install -e ".[dev]"
```

## Example

```python
from primitivebox import PrimitiveBoxClient

client = PrimitiveBoxClient("http://localhost:8080", sandbox_id="sb-12345678")
print(client.health())
print(client.fs.read("README.md"))
```
