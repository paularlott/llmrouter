# Scriptling Router Library

The `router` library is automatically available in routing scripts. It exposes live provider and model data so scripts can make informed routing decisions.

## Configuration

```toml
[smart_routing]
enabled = true
script = "router.scriptling"
default_model = "mistralai/ministral-3-3b"
```

Smart routing activates when a client requests the model name `auto`. The script picks a provider and model. On any failure or empty result, `default_model` is used. The `auto` model is injected into `/v1/models` so clients can discover it.

### Provider Tags

Tags are arbitrary strings you assign to providers and individual models for use in routing scripts.

```toml
[[providers]]
name = "mistral"
provider = "mistral"
token = "..."
enabled = true
tags = ["fast", "cheap"]          # provider-level tags

[providers.model_tags]
"mistralai/ministral-3-3b"     = ["small", "fast", "cheap"]
"mistralai/mistral-small-latest" = ["small", "fast"]

[[providers]]
name = "openai"
provider = "openai"
token = "..."
enabled = true
tags = ["capable", "expensive"]

[providers.model_tags]
"gpt-4o"   = ["capable", "expensive"]
"gpt-4o-mini" = ["fast", "cheap"]
```

---

## Function Reference

| Function | Signature | Returns | Description |
|----------|-----------|---------|-------------|
| `router.set_model` | `set_model(model_id)` | — | Set the model to route to; provider selected automatically |
| `router.get_request` | `get_request()` | `dict` | Current routing request (`type`, `messages`, `tools`) |
| `router.is_chat_completion` | `is_chat_completion()` | `bool` | True if this is a `/v1/chat/completions` request |
| `router.is_responses` | `is_responses()` | `bool` | True if this is a `/v1/responses` request |
| `router.providers` | `providers(**kwargs)` | `list[dict]` | Healthy providers. Optional `tag=str` to filter by provider tag |
| `router.models_for_provider` | `models_for_provider(name, **kwargs)` | `list[str]` | Model IDs for a provider. Optional `tag=str` to filter by model tag |
| `router.models_by_tag` | `models_by_tag(tag)` | `list[str]` | All model IDs that have the given tag |
| `router.model_tags` | `model_tags(model_id)` | `list[str]` | Tags assigned to a model |
| `router.has_model` | `has_model(model_id)` | `bool` | True if the model is available from any provider |
| `router.provider_load` | `provider_load(name)` | `int` | Active completions for a provider (`-1` if not found) |
| `router.message_content_types` | `message_content_types()` | `list[str]` | Unique content part types across all messages (e.g. `"text"`, `"image_url"`) |
| `router.total_tokens_estimate` | `total_tokens_estimate()` | `int` | Rough token estimate (chars/4) across all messages |
| `router.models_by_tags` | `models_by_tags(tags)` | `list[str]` | Model IDs that have ALL of the given tags |
| `router.provider_for_model` | `provider_for_model(model_id)` | `str` | Provider name for a model (first if multiple, `""` if not found) |
| `router.random_model` | `random_model(tag)` | `str` | Weighted random model with the given tag (`""` if none) |

### Provider dict fields

Each item returned by `providers()` contains:

| Field | Type | Description |
|-------|------|-------------|
| `name` | `str` | Provider name from config |
| `type` | `str` | `openai` \| `claude` \| `gemini` \| `ollama` \| `mistral` \| `zai` |
| `load` | `int` | Active completions count |
| `weight` | `float` | Configured weight (0.0–2.0) |
| `tags` | `list[str]` | Provider-level tags |
| `models` | `list[str]` | Model IDs available from this provider |

---

## Script Interface

### Input

The script has access to the request via `router.get_request()` or the raw `request_json` variable:

```python
import router

req = router.get_request()
# req["type"]     - "chat" or "responses"
# req["messages"] - list of {"role": str, "content": str|list}  (chat only)
# req["tools"]    - list of {"type": str, "name": str}           (chat only)
```

Or check the type directly:

```python
if router.is_chat_completion():
    # handle chat request
elif router.is_responses():
    # handle responses request
```

### Output

Call `router.set_model(model_id)` to select the model. The router then picks the best provider automatically using load-balanced routing. Alternatively set the `output_model` variable:

```python
# Preferred: use set_model
router.set_model("mistralai/ministral-3-3b")

# Alternative: set variable
output_model = "mistralai/ministral-3-3b"
```

Return nothing or don't call `set_model` to fall back to `default_model`.

---

## Hot Reload

The routing script is watched with a filesystem watcher. Changes are picked up within ~100ms — no restart required.

---

## Example Scripts

### Route vision requests to a capable model

```python
import router

if "image_url" in router.message_content_types():
    models = router.models_by_tag("vision")
else:
    models = router.models_by_tag("cheap")

if models:
    router.set_model(models[0])
```

### Narrow by secondary tag

```python
import router

# Get all capable models, then prefer the fastest among them
capable = router.models_by_tag("capable")
fast = [m for m in capable if "super_fast" in router.model_tags(m)]

models = fast if fast else capable
if models:
    router.set_model(models[0])
```

### Route by model tag

```python
import router

req = router.get_request()

if router.is_chat_completion() and len(req["tools"]) > 0:
    models = router.models_by_tag("capable")
else:
    models = router.models_by_tag("cheap")

if models:
    router.set_model(models[0])
```

### Route by provider tag

```python
import router

providers = router.providers(tag="cheap")
if providers:
    models = router.models_for_provider(providers[0]["name"])
    if models:
        router.set_model(models[0])
```

### Route by message length

```python
import router

req = router.get_request()
total_chars = sum(len(m["content"]) for m in req["messages"] if isinstance(m["content"], str))

tag = "small" if total_chars < 500 else "capable"
models = router.models_by_tag(tag)
if models:
    router.set_model(models[0])
```

### Prefer least-loaded provider's first model

```python
import router

best = None
for p in router.providers():
    if len(p["models"]) > 0:
        if best is None or p["load"] < best["load"]:
            best = p

if best:
    router.set_model(best["models"][0])
```

### Weighted random selection

```python
import router

model = router.random_model("cheap")
if model:
    router.set_model(model)
```

---

## Fallback Behaviour

| Condition | Result |
|-----------|--------|
| Script calls `router.set_model(model_id)` with a valid model | Route to that model (provider auto-selected) |
| Script sets `output_model` variable | Route to that model (provider auto-selected) |
| Script returns without setting a model | Use `default_model` |
| Script errors or times out (5s limit) | Use `default_model` |
| Model not found in any provider | Use `default_model` |
| `default_model` also not found | Return error to client |
