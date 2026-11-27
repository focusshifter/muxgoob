# Codex-first model routing and OrcaRouter

The default `ai_provider` is `openai-codex`: OpenAI GPT models use Codex OAuth;
non-OpenAI models use OrcaRouter with **their own** `orcarouter_api_key`.
For example, `openai/gpt-6.1-sol` uses Codex and
`anthropic/claude-opus-5.5` uses OrcaRouter. Bare `gpt-*` names still use Codex.
Explicit `ai_provider: openrouter`, `orcarouter`, or `openai` retains that
provider's existing behavior. No settings migration or production write occurs
merely by adding a key or installing this code, but **upgrading changes the
non-OpenAI route under existing `openai-codex` settings from OpenRouter to Orca**.

Codex request-error fallback remains OpenRouter under its existing policy. It
has not been changed to OrcaRouter: the new rule selects non-OpenAI model
workloads, not fallback for OpenAI GPT requests. OpenAI non-GPT models retain
the existing non-Codex handling; this does not claim Codex supports every OpenAI
model. Legacy `openrouter/<model>` IDs under Codex retain their OpenRouter route.

## Owner commands and per-model overrides

Securely add the key to the bot configuration and restart to load it. Commands
remain restricted to the configured bot owner; Telegram chat-admin status does
not grant cross-chat configuration permission.

```text
!ai model reply anthropic/claude-opus-5.5 <chat_id>
!ai model vision google/<vision-model> <chat_id>
!ai model selfprompt openai/gpt-5.4 <chat_id>
!spotify model openai/gpt-6.1-sol anthropic/claude-opus-5.5
!ai get <chat_id>
!ai chat <chat_id>
```

No global provider switch is needed. Vision resolves its independent
`image_vision_model`, not the reply or image-generation model. Selfprompt and
compression resolve their own effective model (plugin and CLI); existing
`selfprompt.model`/legacy compression-setting precedence is unchanged. Spotify
resolves each writer and judge separately and stores the exact configured full
model ID as `generated_by_model`, never a response alias or judge ID.

Optional colon prefixes override the route for just one configured model:

```text
!ai model reply orcarouter:google/<model> <chat_id>
!ai model reply orcarouter:orcarouter/auto <chat_id>
!ai model vision openrouter:google/<vision-model> <chat_id>
!spotify model openai/gpt-6.1-sol orcarouter:anthropic/claude-opus-5.5
```

`allmodels:`, `orcarouter:` and `openrouter:` are routing prefixes.
See [AllModels](allmodels.md) for the explicit-only optional route and its ZDR policy. They are split
**exactly once**; an empty routed ID fails before network IO. An unknown prefix
is an opaque model token, not permission to choose an endpoint or key. A slash
is not the new routing syntax: native Orca IDs `orcarouter/auto` and
`orcarouter/free` remain intact. Vendor IDs are not translated (for example,
`x-ai/...` is not silently rewritten to `grok/...`). Model availability and
feature support must be checked against the actual provider catalog.

## Online search

Append `:online` to enable search without changing the model's route:

```text
!ai model reply grok/grok-4.6:online <chat_id>
!ai model reply deepseek/deepseek-v4.1-flash:online <chat_id>
```

The Orca adapter removes only the terminal suffix before the API request:

- Grok and explicitly Orca-routed OpenAI models use Responses `web_search`.
- Claude uses Chat Completions `web_search_options`.
- Gemini uses the reserved `googleSearch` native tool.
- DeepSeek, Qwen, Kimi/Moonshot and MiniMax use the adapter's reserved search
  function backed by Orca's pinned `branddev.web-search` tool. Only that search
  tool executes internally; ordinary Gooby tools still run in the host tool loop.

Generic search checks the live tool schema, health and price before spending,
allows at most two searches per completion call, caps the quoted unit price at
USD 0.003 and returns at most five bounded URL/title/description sources. The
budget resets when Gooby re-enters the completion client after a host tool; this
is not an account-wide spend limit. Search results are untrusted evidence, never
instructions. Native search has provider-specific billing. Search failure does
not silently become an offline answer, and paid tool POSTs are not retried.

Unknown families and `orcarouter/auto:online` are rejected rather than claiming
unverified search capability. Non-online IDs pass through unchanged. Codex's
native-search behavior and explicit OpenRouter behavior remain separate.
Vendor IDs still must match the Orca catalog: `x-ai/...` is not `grok/...`.

The real unpublished smoke covers both Grok native search and DeepSeek generic
search, followed by host-tool invocation and continuation. Opt in separately:

```bash
# Supply ORCAROUTER_API_KEY securely, never in command history.
ORCAROUTER_ONLINE_LIVE=1 ORCAROUTER_TEST_MODEL=grok/grok-4.6:online GOFLAGS=-tags=sqlite_fts5 mise exec -- go test ./internal/orcarouter -run '^TestOnlineLiveGrok' -count=1 -v
ORCAROUTER_GENERIC_ONLINE_LIVE=1 ORCAROUTER_GENERIC_TEST_MODEL=deepseek/deepseek-v4.1-flash:online GOFLAGS=-tags=sqlite_fts5 mise exec -- go test ./internal/orcarouter -run '^TestOnlineLiveGeneric' -count=1 -v
```

Omit `<chat_id>` only when deliberately changing a global setting. Existing
per-chat overrides take precedence. Model/provider choices for independent
workloads are not overwritten by setting the main reply model.

## Image generation and composition

Image generation and prompt composition have separate provider/model settings:

```text
!ai provider image-prompt openai-codex <chat_id>
!ai model image-prompt anthropic/<tool-capable-model> <chat_id>
!ai model image orcarouter:<dedicated-image-model> <chat_id>
```

These settings do not enable generation or composition: existing opt-in image
permissions and composer modes still apply. Explicit image providers
`openai-codex`, `openrouter`, and `orcarouter` remain available. Colon overrides
work on the administrator-configured image model; the LLM cannot choose an
endpoint or substitute a model through tool arguments.

Orca dedicated image requests use `/v1/images/generations`. Gemini chat-based
image generation is **not supported**. Explicit JPEG/WebP `output_format`
requests are **not supported** by this Orca adapter; keep the default provider
format. Returned image bytes determine the actual file extension, including
JPEG/WebP when returned by the provider.

After a successful generation response, download/decoding/preparation failures
are not generation failures: no second paid generation POST is attempted. URL
retrieval is bounded to two GET attempts against the original URL within the
request context; failure locks the tool against later regeneration in the same
workload. Signed URLs are not exposed in errors. Successful generation followed
by Telegram delivery failure retains the generated file and the one-generation
guard as before.

## Credential boundaries and research exception

Orca chat calls use `https://api.orcarouter.ai/v1/chat/completions` with Bearer
auth. Missing Orca credentials fail before network IO: no borrowing the
OpenRouter key and no silent fallback to another provider. No real credential
is included in the distribution config, and normal tests never call Orca.

**Spotify Sonar criticism research remains explicitly on OpenRouter**, with
`openrouter_api_key`, even with Orca writers/judge. `plugins/spotify/research.go`
depends on OpenRouter's citation envelope. This change does not reroute research,
rewrite historical reviews, or publish a live smoke review.

This is not generic Orca API access (audio, embeddings, fine tuning, billing,
model discovery, or streaming). Local tests prove routing and guards, not live
model availability or tools/vision/schema support.

## Verification

```bash
GOFLAGS=-tags=sqlite_fts5 mise exec -- go test ./...
GOFLAGS=-tags=sqlite_fts5 mise exec -- go vet ./...
```

The unpublished live smoke is separately gated, bounded to 45 seconds and two
64-token completion requests with one inert local tool result. It does not send
to Telegram or publish reviews. It requires a real key and may cost money;
never put credentials in shell history.

```bash
# Supply ORCAROUTER_API_KEY securely through your environment first.
ORCAROUTER_LIVE_TEST=1 GOFLAGS=-tags=sqlite_fts5 mise exec -- go test ./internal/aiprovider -run '^TestOrcaRouterLiveSmoke$' -count=1 -v
# Optional: ORCAROUTER_TEST_MODEL=<tool-capable-model> (default: orcarouter/auto)
```
