# Scriptling Router Library

The `router` library is automatically available in routing scripts. It exposes live provider and model data so scripts can make informed routing decisions.

## Configuration

```toml
[smart_routing]
enabled = true
script = "router.scriptling"
default_model = "mistralai/ministral-3-3b"
libpath = ["./router_libs", "/shared/libs"]  # Optional: additional directories to search for libraries

[smart_routing.vars]
openai_key = "sk-..."
my_endpoint = "https://api.example.com"
```

Smart routing activates when a client requests the model name `auto`. The script picks a provider and model. On any failure or empty result, `default_model` is used. The `auto` model is injected into `/v1/models` so clients can discover it.

`vars` is an optional map of key-value string pairs made available to the script as the `vars` library (see [Script Variables](#script-variables)).

### Library Path

Libraries are searched in the following order:
1. **Script directory** - the directory containing the routing script is always searched first
2. **libpath entries** - any directories specified in `libpath` are searched in order

This matches the behaviour of the scriptling CLI's `--libpath` / `-L` flag. The script directory is implicitly the first search path, allowing libraries placed alongside the script to be imported without additional configuration.

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

## Available Libraries

The following libraries are pre-registered and available for `import` in every routing script.

### Standard Libraries

All Scriptling standard libraries are available without any configuration:

| Library                   | Description                             |
| ------------------------- | --------------------------------------- |
| `base64`                  | Base64 encoding/decoding                |
| `collections`             | Specialised container datatypes         |
| `contextlib`              | Utilities for the `with` statement      |
| `datetime`                | Date and time formatting                |
| `difflib`                 | Sequence comparison and diff generation |
| `functools`               | Higher-order functions                  |
| `hashlib`                 | Secure hash algorithms                  |
| `html`                    | HTML escaping/unescaping                |
| `io`                      | In-memory I/O streams                   |
| `itertools`               | Iterator functions                      |
| `json`                    | JSON parsing and generation             |
| `math`                    | Mathematical functions and constants    |
| `platform`                | Platform identifying data               |
| `random`                  | Random number generation                |
| `re`                      | Regular expression operations           |
| `statistics`              | Statistical functions                   |
| `string`                  | String constants                        |
| `textwrap`                | Text wrapping and filling               |
| `time`                    | Time access and conversions             |
| `urllib` / `urllib.parse` | URL handling                            |
| `uuid`                    | UUID generation                         |

### Extended Libraries

The following extended libraries are enabled. Filesystem access (`os`, `os.path`, `pathlib`, `glob`), subprocess execution (`subprocess`), and async resource waiting (`wait_for`) are **not** available.

| Library       | Description                             |
| ------------- | --------------------------------------- |
| `requests`    | HTTP client (GET, POST, etc.)           |
| `secrets`     | Cryptographically strong random numbers |
| `html.parser` | HTML/XHTML parser                       |
| `logging`     | Logging to the router log               |
| `yaml`        | YAML parsing and generation             |
| `toml`        | TOML parsing and generation             |
| `sys`         | System parameters (argv, stdin)         |

### Scriptling Libraries

| Library                   | Description                                    |
| ------------------------- | ---------------------------------------------- |
| `scriptling.ai`           | AI/LLM client for OpenAI-compatible APIs       |
| `scriptling.ai.agent`     | Agentic AI loop with automatic tool execution  |
| `scriptling.mcp`          | MCP (Model Context Protocol) tool interaction  |
| `scriptling.toon`         | TOON encoding/decoding                         |
| `scriptling.similarity`       | String matching and similarity utilities        |
| `scriptling.template.html`   | HTML template rendering                         |
| `scriptling.template.text`   | Text template rendering                         |
| `scriptling.runtime`         | Background tasks                                |
| `scriptling.runtime.kv`   | Thread-safe key-value store                    |
| `scriptling.runtime.sync` | Named cross-environment concurrency primitives |

### Script Variables

Key-value pairs defined in `[smart_routing.vars]` are exposed as the `vars` library:

```python
import vars

client = scriptling.ai.Client("", api_key=vars.openai_key)
```

All values are strings. Use this to pass tokens, endpoints, or other configuration to the script without hard-coding them.

---

## Function Reference

| Function                       | Signature                             | Returns      | Description                                                                                       |
| ------------------------------ | ------------------------------------- | ------------ | ------------------------------------------------------------------------------------------------- |
| `router.set_model`             | `set_model(model_id, hint=provider)`  | —            | Set the model to route to; `hint` optionally suggests a provider (ignored if overloaded)          |
| `router.get_request`           | `get_request()`                       | `dict`       | Current routing request (`type`, `messages`, `tools`)                                             |
| `router.is_chat_completion`    | `is_chat_completion()`                | `bool`       | True if this is a `/v1/chat/completions` request                                                  |
| `router.is_responses`          | `is_responses()`                      | `bool`       | True if this is a `/v1/responses` request                                                         |
| `router.providers`             | `providers(**kwargs)`                 | `list[dict]` | Healthy providers. Optional `tag=str` to filter by provider tag                                   |
| `router.models_for_provider`   | `models_for_provider(name, **kwargs)` | `list[str]`  | Model IDs for a provider. Optional `tag=str` to filter by model tag                               |
| `router.models_by_tag`         | `models_by_tag(tag)`                  | `list[str]`  | All model IDs that have the given tag                                                             |
| `router.model_tags`            | `model_tags(model_id)`                | `list[str]`  | Tags assigned to a model                                                                          |
| `router.has_model`             | `has_model(model_id)`                 | `bool`       | True if the model is available from any provider                                                  |
| `router.provider_load`         | `provider_load(name)`                 | `int`        | Active completions for a provider (`-1` if not found)                                             |
| `router.message_content_types` | `message_content_types()`             | `list[str]`  | Unique content part types across all messages (e.g. `"text"`, `"image_url"`)                      |
| `router.total_tokens_estimate` | `total_tokens_estimate()`             | `int`        | Estimated prompt token count across all messages (accounts for content parts, images, tool calls) |
| `router.models_by_tags`        | `models_by_tags(tags)`                | `list[str]`  | Model IDs that have ALL of the given tags                                                         |
| `router.providers_for_model`   | `providers_for_model(model_id)`       | `list[dict]` | All healthy providers serving a model, each with `name`, `type`, `load`, `weight`, `tags`         |
| `router.random_model`          | `random_model(tag)`                   | `str`        | Weighted random model with the given tag (`""` if none)                                           |
| `router.provider_healthy`      | `provider_healthy(name)`              | `bool`       | True if the provider exists, is enabled, and is healthy                                           |
| `router.has_provider`          | `has_provider(name)`                  | `bool`       | True if the provider exists in config (regardless of health)                                      |
| `router.model_load`            | `model_load(model_id)`                | `int`        | Total active completions across all healthy providers serving the model                           |
| `router.system_prompt`         | `system_prompt()`                     | `str`        | Content of the system message, or `""` if none                                                    |
| `router.last_message`          | `last_message()`                      | `str`        | Content of the last user message, or `""` if none                                                 |
| `router.conversation_turns`    | `conversation_turns()`                | `int`        | Number of user turns in the conversation                                                          |

### Provider dict fields

Each item returned by `providers()` contains:

| Field    | Type        | Description                                                        |
| -------- | ----------- | ------------------------------------------------------------------ |
| `name`   | `str`       | Provider name from config                                          |
| `type`   | `str`       | `openai` \| `claude` \| `gemini` \| `ollama` \| `mistral` \| `zai` |
| `load`   | `int`       | Active completions count                                           |
| `weight` | `float`     | Configured weight (0.0–2.0)                                        |
| `tags`   | `list[str]` | Provider-level tags                                                |
| `models` | `list[str]` | Model IDs available from this provider                             |

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

Call `router.set_model(model_id)` to select the model. Pass `hint=provider_name` to suggest a specific provider — the router honours the hint unless another provider has a significantly lower load (score more than 1.0 better). Alternatively set the `output_model` variable:

```python
# Provider auto-selected by load balancing
router.set_model("mistralai/ministral-3-3b")

# Suggest a specific provider (hint ignored if it's overloaded)
router.set_model("mistralai/ministral-3-3b", hint="mistral-eu")

# Alternative: set variable (no hint support)
output_model = "mistralai/ministral-3-3b"
```

Return nothing or don't call `set_model` to fall back to `default_model`.

---

## Hot Reload

The routing script and any libraries in the library search paths are watched with a filesystem watcher. Changes are picked up within ~100ms — no restart required. When any file in a watched directory changes, all VM pools are rebuilt with the updated libraries.

---

## Script Libraries (`libpath`)

Set `libpath` to a list of directories containing `.py` files to make shared libraries available to routing scripts. The directory containing the routing script is always searched first, followed by any `libpath` entries in order.

```toml
[smart_routing]
enabled = true
script = "router.py"
libpath = ["./router_libs", "/shared/libs"]
```

Given `./router_libs/utils.py`:

```python
def pick_cheapest(models):
    return models[0] if models else ""
```

The routing script can then import it:

```python
import router
import utils

models = router.models_by_tag("cheap")
model = utils.pick_cheapest(models)
if model:
    router.set_model(model)
```

Libraries are hot-reloaded alongside the routing script — any change to a file in a watched directory triggers a full pool rebuild.

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

### Pick model with free capacity, fall back to least-loaded alternative

```python
import router

def has_free_capacity(model_id, max_load=2):
    return any(p["load"] < max_load for p in router.providers_for_model(model_id))

# Try preferred models in order; pick first with free capacity
for model in router.models_by_tag("capable"):
    if has_free_capacity(model):
        router.set_model(model)
        break
else:
    # All capable models busy — fall back to least-loaded cheap model
    best_model, best_load = None, None
    for model in router.models_by_tag("cheap"):
        load = min((p["load"] for p in router.providers_for_model(model)), default=None)
        if load is not None and (best_load is None or load < best_load):
            best_model, best_load = model, load
    if best_model:
        router.set_model(best_model)
```

### Weighted random selection

```python
import router

model = router.random_model("cheap")
if model:
    router.set_model(model)
```

### Route based on system prompt content

```python
import router

prompt = router.system_prompt()
if "code" in prompt or "programming" in prompt:
    models = router.models_by_tag("capable")
else:
    models = router.models_by_tag("cheap")

if models:
    router.set_model(models[0])
```

### Route long conversations to a large-context model

```python
import router

if router.conversation_turns() > 10 or router.total_tokens_estimate() > 4000:
    models = router.models_by_tag("large_context")
else:
    models = router.models_by_tag("cheap")

if models:
    router.set_model(models[0])
```

### Skip busy models, route to least-loaded alternative

```python
import router

preferred = "gpt-4o"
if router.model_load(preferred) < 3 and router.has_model(preferred):
    router.set_model(preferred)
else:
    # fall back to least-loaded capable model
    best, best_load = None, None
    for m in router.models_by_tag("capable"):
        load = router.model_load(m)
        if best_load is None or load < best_load:
            best, best_load = m, load
    if best:
        router.set_model(best)
```

### Route based on last message keyword

```python
import router

msg = router.last_message().lower()
if any(w in msg for w in ["image", "photo", "picture", "diagram"]):
    models = router.models_by_tag("vision")
else:
    models = router.models_by_tag("cheap")

if models:
    router.set_model(models[0])
```

---

## Fallback Behaviour

| Condition                                                    | Result                                                    |
| ------------------------------------------------------------ | --------------------------------------------------------- |
| Script calls `router.set_model(model_id)` with a valid model | Route to that model; hint provider used if not overloaded |
| Script sets `output_model` variable                          | Route to that model (provider auto-selected)              |
| Script returns without setting a model                       | Use `default_model`                                       |
| Script errors or times out (5s limit)                        | Use `default_model`                                       |
| Model not found in any provider                              | Use `default_model`                                       |
| `default_model` also not found                               | Return error to client                                    |
