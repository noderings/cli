#!/usr/bin/env bash
# Fail if the generated API client contains internal details or API surface
# the CLI does not call. Run by `make generate-api-client`, `make leak-check`
# and CI.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "${ROOT}"

GEN_DIR="internal/api/generated"
SPEC="${GEN_DIR}/openapi3.yaml"
ALLOWLIST="scripts/openapi-allowlist.txt"

status=0
fail() {
  echo "openapi-leak-check: $1" >&2
  status=1
}

# 1. Internal infrastructure, credentials, and admin or billing surface.
PATTERNS=(
  '\.svc\b'
  'cluster\.local'
  '\.internal\b'
  '\blocalhost\b'
  '\b127\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\b'
  '\b10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\b'
  '\b172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3}\b'
  '\b192\.168\.[0-9]{1,3}\.[0-9]{1,3}\b'
  '[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}'
  '/v1/admin'
  '[Aa]dmin[A-Z_]'
  '[Ee]nforcement'
  '[Ii]mpersonat'
  '[Xx]-[Aa]cting-[Uu]ser'
  '[Ss]tripe'
  '[Pp]ay[Pp]al'
  '[Ww]ebhook'
  '[Pp]ayout'
  '[Ii]nvoice'
  '[Uu]sageReport'
  '[Rr]ecordUsage'
  'ConnectAccount'
  '[Pp]aymentMethod|payment_method'
  '[Hh]ookScript'
  'VNCConsole'
  '\b(session_token|client_secret|sessionToken|clientSecret)\b'
  '\b[Pp]assword\b'
)
# Maintainers can keep extra patterns (internal component or host names) in a
# file outside this repository, one extended regex per line.
if [[ -n "${OPENAPI_LEAK_EXTRA_PATTERNS_FILE:-}" ]]; then
  while IFS= read -r line; do
    [[ -z "${line}" || "${line}" == \#* ]] || PATTERNS+=("${line}")
  done <"${OPENAPI_LEAK_EXTRA_PATTERNS_FILE}"
fi

FILES=("${SPEC}" "${GEN_DIR}"/*.go)

# Public NodeRings URLs are allowed. Remove only the URL text before matching,
# so anything else on the same line is still checked.
scan() {
  local pattern="$1"
  local f
  for f in "${FILES[@]}"; do
    sed -E 's#https://(api\.)?noderings\.com##g' "${f}" | grep -nE -- "${pattern}" | sed "s#^#${f}:#" || true
  done
}

for p in "${PATTERNS[@]}"; do
  hits="$(scan "${p}")"
  if [[ -n "${hits}" ]]; then
    fail "pattern ${p} found in ${GEN_DIR}:"
    head -20 <<<"${hits}" >&2
  fi
done

# 2. Free text copied from backend proto comments must be stripped from the
#    spec (scripts/openapi-allowlist.py does this). Response objects keep an
#    empty description; the info title is the public API name.
hits="$(grep -nE "^[[:space:]]*(description|summary|example|examples|externalDocs):[[:space:]]*[^[:space:]]" "${SPEC}" \
  | grep -vE ":[[:space:]]*description: ''$" || true)"
hits+="$(grep -nE "^[[:space:]]+title:[[:space:]]*[^[:space:]]" "${SPEC}" | grep -vE "^[0-9]+:  title: NodeRings API$" || true)"
if [[ -n "${hits}" ]]; then
  fail "free text found in ${SPEC} (regenerate with make generate-api-client):"
  head -20 <<<"${hits}" >&2
fi

# 3. Every operation in the spec and in the generated client must be on the
#    allow-list, and every allow-listed operation must be generated and called.
allowed="$(grep -vE '^[[:space:]]*(#|$)' "${ALLOWLIST}" | sed 's/[[:space:]]//g' | sort -u)"
allowed_go="$(echo "${allowed}" | tr -d '_' | sort -u)"
in_spec="$(grep -oE '^[[:space:]]+operationId:[[:space:]]*[A-Za-z0-9_]+' "${SPEC}" | awk '{print $2}' | sort -u)"
in_client="$(grep -oE '^func \(c \*Client\) [A-Z][A-Za-z0-9]+\(' "${GEN_DIR}/client.gen.go" \
  | sed -E 's/^func \(c \*Client\) //; s/\($//; s/WithBody$//' | sort -u)"

extra="$(comm -13 <(echo "${allowed}") <(echo "${in_spec}"))"
[[ -z "${extra}" ]] || { fail "operations in ${SPEC} not on ${ALLOWLIST}:"; echo "${extra}" >&2; }
extra="$(comm -13 <(echo "${allowed_go}") <(echo "${in_client}"))"
[[ -z "${extra}" ]] || { fail "client methods in ${GEN_DIR}/client.gen.go not on ${ALLOWLIST} (stale client? run make generate-api-client):"; echo "${extra}" >&2; }
missing="$(comm -23 <(echo "${allowed}") <(echo "${in_spec}"))"
[[ -z "${missing}" ]] || { fail "allow-listed operations missing from ${SPEC} (run make generate-api-client):"; echo "${missing}" >&2; }
missing="$(comm -23 <(echo "${allowed_go}") <(echo "${in_client}"))"
[[ -z "${missing}" ]] || { fail "allow-listed operations missing from ${GEN_DIR}/client.gen.go (run make generate-api-client):"; echo "${missing}" >&2; }

# Keep the allow-list minimal: each operation must be called outside the
# generated package.
src="$(find internal cmd -name '*.go' -not -path "*/${GEN_DIR#internal/}/*" -print0 | xargs -0 cat)"
while IFS= read -r op; do
  [[ -n "${op}" ]] || continue
  if ! grep -qE "\b(New|Parse)?${op}(WithBody)?(WithResponse|Request|Response|JSONRequestBody|Params)?\b" <<<"${src}"; then
    fail "allow-listed operation ${op} is not called by the CLI; remove it from ${ALLOWLIST}"
  fi
done <<<"${allowed_go}"

if [[ "${status}" -eq 0 ]]; then
  echo "openapi-leak-check: ok"
fi
exit "${status}"
