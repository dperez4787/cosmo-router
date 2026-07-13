# cosmo-router

Custom [WunderGraph Cosmo](https://cosmo-docs.wundergraph.com/) router build for the
[imdb-federation](https://github.com/dperez4787/imdb-federation) subgraphs: the upstream
router Go module plus this repo's custom modules. The first module (`requestlog`) exists to
prove the custom-module pipeline; the real payload — field-level authorization backed by a
centralized policy service — lands as the next module on the same scaffolding.
## Live UIs

Every user-facing surface in this system, live:

| Surface | URL |
|---|---|
| **Marquee** — the IMDb browser | https://dfp-imdb-browser.web.app/titles |
| **IMDb Graph Governance** — field-level policy control plane | https://imdb-policy-service-dkuqnmldta-uc.a.run.app/ |
| **linear-example** — records app + the engineering blog | https://project-d60a83c1-2c60-4d51-ad0.web.app/ · [blog](https://project-d60a83c1-2c60-4d51-ad0.web.app/blog) |

## Architecture

The router runs in **standalone mode** (no Cosmo Cloud control plane):

1. `scripts/compose.sh` fetches the federated SDL from each **deployed** subgraph
   (`{ _service { sdl } }`) and generates `graph.yaml` with the production routing URLs.
2. `wgc router compose` statically composes those into `execution-config.json`.
3. The Dockerfile bakes the execution config and `config/config.yaml` into the image.
4. Cloud Run serves the image as `cosmo-router` (us-central1, same project as the subgraphs).

Because the execution config is baked into the image, **subgraph schema changes require a
router redeploy**: run the Deploy workflow (`gh workflow run deploy.yml`) or send a
`repository_dispatch` event of type `subgraphs-updated`. Moving to the Cosmo Cloud control
plane later replaces steps 1–3 with `wgc subgraph publish` + CDN polling, and only touches
`execution_config` in the router config.

## CI/CD

- `ci.yml` (PRs + main): `go build` / `go vet`, plus a composition check against the live subgraphs.
- `deploy.yml` (push to main, `workflow_dispatch`, `repository_dispatch: subgraphs-updated`):
  compose → build/push image to Artifact Registry → `gcloud run deploy` → federated smoke test.

Auth is Workload Identity Federation (no service-account keys); GitHub secrets
`WIF_PROVIDER` and `DEPLOY_SA` come from `terraform output` in `infra/`.

## Local development

Requirements: Docker, Node 20+ (for `wgc`), `jq`. No local Go needed — the binary builds in Docker.

```bash
./scripts/compose.sh                       # fetch SDLs + compose execution-config.json
docker build -t cosmo-router .
docker run --rm -p 3002:3002 cosmo-router  # routes to the *deployed* subgraphs
# GraphiQL: http://localhost:3002
```

Example federated query (spans titles, ratings, crew, episodes):

```graphql
{
  title(tconst: "tt0944947") {
    primaryTitle
    rating { averageRating numVotes }
    directors { primaryName }
    episodes(limit: 3) { primaryTitle episode { seasonNumber episodeNumber } }
  }
}
```

## Custom modules

Modules live in `modules/`, register themselves in `init()` via `core.RegisterModule`, and are
imported (blank import) in `cmd/router/main.go`. Module config lives under `modules.<id>` in
`config/config.yaml`. The router version is pinned in `go.mod` via a pseudo-version pointing at
the upstream `router@x.y.z` release tag (upstream tags aren't Go-semver).

## Infrastructure

`infra/` is Terraform for IAM only: the repo-scoped WIF provider, the deploy/runtime service
accounts, and the Artifact Registry repo. The Cloud Run service itself is created by
`gcloud run deploy` in CI, matching imdb-federation's pattern. State lives in the shared
GCS bucket under the `cosmo-router` prefix.
