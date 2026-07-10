#!/usr/bin/env bash
# Exercises every federation entity resolver through the router.
#
# Entity resolvers (where a subgraph answers _entities fetches):
#   Title @key(tconst): titles, ratings, crew, episodes, akas, principals
#   Name  @key(nconst): names, principals
#
# Each test enters the target subgraph via an entity fetch — a query rooted
# in a DIFFERENT subgraph that selects fields the target owns — so the
# router must issue the _entities call under test.
#
# Usage: entity-test.sh [router-url]   (default http://localhost:3002)
# The router requires a Google-signed ID token; set TOKEN to override the
# default of your gcloud user identity.
set -euo pipefail

URL="${1:-http://localhost:3002}/graphql"
TOKEN="${TOKEN:-$(gcloud auth print-identity-token 2>/dev/null || true)}"
TCONST="tt0944947" # Game of Thrones: series with episodes, akas, crew, principals
FAIL=0

q() { # q <label> <query> <jq assertion>
  local label="$1" query="$2" assert="$3" resp
  resp=$(curl -sf --max-time 60 -X POST "$URL" \
    -H 'Content-Type: application/json' \
    ${TOKEN:+-H "Authorization: Bearer $TOKEN"} \
    -d "$(jq -cn --arg q "$query" '{query: $q}')")
  if echo "$resp" | jq -e "(.errors == null) and ($assert)" >/dev/null; then
    echo "PASS  $label"
  else
    echo "FAIL  $label"
    echo "$resp" | jq -c '{errors, data}' | head -3
    FAIL=1
  fi
}

echo "Router: $URL"

# --- Title entity resolvers, entered from the titles subgraph root ---
q "Title -> ratings    (title.rating)" \
  "{ title(tconst: \"$TCONST\") { rating { averageRating numVotes } } }" \
  '.data.title.rating.averageRating > 0'

q "Title -> crew       (title.directors)" \
  "{ title(tconst: \"$TCONST\") { directors { nconst } } }" \
  '.data.title.directors | length > 0'

q "Title -> episodes   (title.episodes)" \
  "{ title(tconst: \"$TCONST\") { episodes(limit: 2) { tconst } } }" \
  '.data.title.episodes | length > 0'

q "Title -> akas       (title.akas)" \
  "{ title(tconst: \"$TCONST\") { akas { title region } } }" \
  '.data.title.akas | length > 0'

q "Title -> principals (title.principals)" \
  "{ title(tconst: \"$TCONST\") { principals { category name { nconst } } } }" \
  '.data.title.principals | length > 0'

# --- Name entity resolver in names: crew returns Name key stubs
#     (resolvable: false there), so primaryName forces a fetch into names ---
q "Name  -> names      (directors.primaryName via crew stub)" \
  "{ title(tconst: \"$TCONST\") { directors { primaryName } } }" \
  '[.data.title.directors[].primaryName] | any(. != null)'

# A director nconst seeds the two name-rooted tests below.
NCONST=$(curl -sf --max-time 60 -X POST "$URL" -H 'Content-Type: application/json' \
  ${TOKEN:+-H "Authorization: Bearer $TOKEN"} \
  -d "{\"query\":\"{ title(tconst: \\\"$TCONST\\\") { directors { nconst } } }\"}" \
  | jq -re '.data.title.directors[0].nconst')
echo "seed  nconst=$NCONST"

# --- Title entity resolver in titles: names returns Title key stubs
#     (resolvable: false there), so primaryTitle forces a fetch into titles ---
q "Title -> titles     (name.knownForTitles.primaryTitle via names stub)" \
  "{ name(nconst: \"$NCONST\") { knownForTitles { primaryTitle } } }" \
  '[.data.name.knownForTitles[]?.primaryTitle] | any(. != null)'

# --- Name entity resolver in principals: credits is principals-owned,
#     reached from a names-rooted query ---
q "Name  -> principals (name.credits)" \
  "{ name(nconst: \"$NCONST\") { credits(limit: 2) { category title { tconst } } } }" \
  '.data.name.credits | length > 0'

# --- Depth check: one query bouncing titles -> crew -> names -> principals -> titles ---
q "4-hop chain        (title.directors.credits.title.primaryTitle)" \
  "{ title(tconst: \"$TCONST\") { directors { primaryName credits(limit: 1) { title { primaryTitle } } } } }" \
  '[.data.title.directors[].credits[]?.title.primaryTitle] | any(. != null)'

[ "$FAIL" -eq 0 ] && echo "All entity resolvers OK" || { echo "Entity resolver failures"; exit 1; }
