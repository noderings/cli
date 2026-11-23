#!/usr/bin/env python3
"""Cut an OpenAPI 3 spec down to the operations on an allow-list.

Keeps only the allow-listed operations, the components they reach through
$ref, and the tags they use. Strips free text (descriptions, summaries,
titles, examples) because it is copied from backend proto comments and can
describe internal behaviour. Response objects keep an empty description, which
OpenAPI 3 requires.

Usage: openapi-allowlist.py ALLOWLIST IN.yaml OUT.yaml
"""

import sys

import yaml

HTTP_METHODS = {"get", "put", "post", "delete", "options", "head", "patch", "trace"}
FREE_TEXT = {"description", "summary", "title", "example", "examples", "externalDocs"}
# Maps whose keys are names chosen by the API author, not OpenAPI keywords.
NAME_MAPS = {"properties", "schemas", "parameters", "requestBodies", "responses",
             "securitySchemes", "headers", "content", "paths", "mapping", "scopes"}


def read_allowlist(path):
    ops = set()
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.split("#", 1)[0].strip()
            if line:
                ops.add(line)
    return ops


def refs_in(node, out):
    if isinstance(node, dict):
        for k, v in node.items():
            if k == "$ref" and isinstance(v, str):
                out.add(v)
            elif k == "mapping" and isinstance(v, dict):
                out.update(x for x in v.values() if isinstance(x, str))
            else:
                refs_in(v, out)
    elif isinstance(node, list):
        for v in node:
            refs_in(v, out)


def resolve(spec, ref):
    if not ref.startswith("#/"):
        raise SystemExit(f"openapi-allowlist: external $ref not supported: {ref}")
    node = spec
    for part in ref[2:].split("/"):
        node = node[part.replace("~1", "/").replace("~0", "~")]
    return node


def strip(node, parent_key=None):
    if isinstance(node, dict):
        out = {}
        for k, v in node.items():
            if parent_key not in NAME_MAPS and k in FREE_TEXT:
                continue
            out[k] = strip(v, k)
        if parent_key == "responses":
            for resp in out.values():
                if isinstance(resp, dict) and "$ref" not in resp:
                    resp["description"] = ""
        return out
    if isinstance(node, list):
        return [strip(v, None) for v in node]
    return node


class NoAliasDumper(yaml.SafeDumper):
    def ignore_aliases(self, data):
        return True


def main():
    if len(sys.argv) != 4:
        raise SystemExit(__doc__)
    allowed = read_allowlist(sys.argv[1])
    with open(sys.argv[2], encoding="utf-8") as f:
        spec = yaml.safe_load(f)

    paths, found, tags = {}, set(), set()
    for path, item in spec.get("paths", {}).items():
        kept = {}
        for method, op in item.items():
            if method not in HTTP_METHODS:
                continue
            if op.get("operationId") in allowed:
                kept[method] = op
                found.add(op["operationId"])
                tags.update(op.get("tags", []))
        if kept:
            # Path-level parameters apply to every kept method.
            if "parameters" in item:
                kept["parameters"] = item["parameters"]
            paths[path] = kept

    missing = sorted(allowed - found)
    if missing:
        raise SystemExit("openapi-allowlist: allow-listed operations not in spec:\n  "
                         + "\n  ".join(missing))

    # Follow $refs to a fixed point. Security schemes are referenced by name.
    seen, todo = set(), set()
    refs_in(paths, todo)
    for req in spec.get("security", []):
        todo.update(f"#/components/securitySchemes/{name}" for name in req)
    while todo:
        ref = todo.pop()
        if ref in seen:
            continue
        seen.add(ref)
        refs_in(resolve(spec, ref), todo)

    components = {}
    for section, items in spec.get("components", {}).items():
        kept = {name: body for name, body in items.items()
                if f"#/components/{section}/{name}" in seen}
        if kept:
            components[section] = kept

    out = {"openapi": spec["openapi"], "info": spec["info"]}
    if "servers" in spec:
        out["servers"] = spec["servers"]
    out["tags"] = [t for t in spec.get("tags", []) if t.get("name") in tags]
    out["paths"] = paths
    if "security" in spec:
        out["security"] = spec["security"]
    out["components"] = components

    out = strip(out)
    out["info"]["title"] = spec["info"].get("title", "API")
    with open(sys.argv[3], "w", encoding="utf-8") as f:
        yaml.dump(out, f, Dumper=NoAliasDumper, sort_keys=False, width=1000,
                  allow_unicode=True)
    print(f"openapi-allowlist: kept {len(found)} operations, "
          f"{sum(len(v) for v in components.values())} components")


if __name__ == "__main__":
    main()
