# cosmo-router

Custom Go build of the WunderGraph Cosmo router (standalone mode, no control
plane) fronting the 8 imdb-federation Spring Boot subgraphs on Cloud Run.
Purpose: custom modules — `requestlog`, `subgraphtoken`, and `fieldauth`
(field-level governance enforcing the imdb-policy-service bundle).

## Auth (two boundaries, two token types)

- Client → router: Google-signed ID tokens, verified by stock
  `authentication.jwt` JWKS config in `config/config.yaml` (Firebase tokens for
  the linear-example login, Google OIDC for gcloud users / service accounts —
  audience allowlist includes the router URL and gcloud's fixed client id).
  Unauthenticated operations get 401 (`authorization.require_authentication`).
- Router → subgraphs: Cloud Run IAM. The subgraphs have no `allUsers` invoker;
  the `subgraphtoken` module attaches per-audience ID tokens minted as the
  runtime SA (`cosmorouter-run`, granted `roles/run.invoker` in
  imdb-federation's `infra/subgraph_invokers.tf` — NOT this repo's Terraform).
  Without ADC (plain local runs) it logs a warning and forwards unsigned.

## Commands

- Compose + build: `./scripts/compose.sh && docker build -t cosmo-router .`
  (introspects the LIVE subgraphs — needs run.invoker: your gcloud user
  identity locally, `IMPERSONATE_SA=<deploy-sa>` in CI; plus network + jq)
- Run locally: `docker run --rm -p 3002:3002 cosmo-router` — GraphiQL on
  http://localhost:3002; call with
  `-H "Authorization: Bearer $(gcloud auth print-identity-token)"`
- Test all entity resolvers: `./scripts/entity-test.sh [router-url]`
- Go checks: `go build ./... && go vet ./...`
- Recompose/redeploy after subgraph schema changes: `gh workflow run deploy.yml`

## Rules

- Router version is pinned in `go.mod` as a pseudo-version of the upstream
  `router@x.y.z` tag (upstream tags aren't Go-semver — never `@latest`). Keep
  the tag name in the go.mod comment in sync when bumping.
- `wgc` is pinned in `scripts/compose.sh`; keep it in lockstep with
  imdb-federation's pinned version.
- `graph.yaml`, `sdl/`, `execution-config.json` are GENERATED (gitignored) —
  the source of truth for subgraph names/URLs is `scripts/compose.sh` only.
- Execution config is baked into the image: schema changes need a redeploy
  (deploy.yml also listens for `repository_dispatch: subgraphs-updated`).
- Modules: implement in `modules/<name>`, register via `core.RegisterModule`
  in `init()`, blank-import in `cmd/router/main.go`, config under
  `modules.<id>` in `config/config.yaml` (id, not package name).
- `infra/` Terraform covers IAM/AR/WIF only — never the Cloud Run service
  (CI's `gcloud run deploy` owns it). Shared `github-pool` WIF pool is owned
  by the linear-example stack; APIs are enabled by the imdb-federation stack.
- The smoke test asserts the `X-Imdb-Router` header — it proves the custom
  module chain ran. Don't remove the header without replacing the assertion.
- Field-level governance (`modules/fieldauth`): polls the imdb-policy-service
  bundle (ETag, fail-static — last good bundle survives outages; before the
  FIRST successful fetch it fails open and logs), walks each operation's
  coordinates against `engineConfig.graphqlSchema` from the baked execution
  config, and rejects violations with `PERMISSION_DENIED` (403). Roles come
  from the JWT `roles` claim (policy-service persona tokens) or the bundle's
  `principals` map (Google identities). `mode: log-only` is the rollout
  switch. The governance smoke test asserts deny-without-role AND
  allow-as-analyst — keep both sides.
- The policy service JWKS entry in `authentication.jwt` means the router
  won't start cleanly unless imdb-policy-service is reachable — deploy the
  policy service before shipping router config changes that reference it.
