# cosmo-router

Custom Go build of the WunderGraph Cosmo router (standalone mode, no control
plane) fronting the 7 imdb-federation Spring Boot subgraphs on Cloud Run.
Purpose: custom modules — `requestlog` today, field-level authorization via a
centralized policy service next.

## Commands

- Compose + build: `./scripts/compose.sh && docker build -t cosmo-router .`
  (compose fetches SDL from the LIVE deployed subgraphs; needs network + jq)
- Run locally: `docker run --rm -p 3002:3002 cosmo-router` — GraphiQL on
  http://localhost:3002, routes to the deployed Cloud Run subgraphs
- Go checks (CI does this; no local Go assumed): `go build ./... && go vet ./...`
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
