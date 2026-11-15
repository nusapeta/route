# Summary

What does this change?

## Type

- [ ] Bug fix
- [ ] Feature
- [ ] Documentation
- [ ] Data or graph
- [ ] CI or build

## Evidence

For routing behavior changes, include the `from`/`to` pair and
`nusa-graphinfo` output (format, nodes, edges, PBF hash).

```text
from= to=
graphinfo:
```

For performance claims, include machine, commit, and reproduction command.

## Checklist

- [ ] `gofmt`, `go vet`, `go test -race`, `go build` pass.
- [ ] A regression test exists for the behavior bug.
- [ ] Contract documentation is updated when needed.
- [ ] No PBF, graph, or large data file is included.
- [ ] Commits carry `Signed-off-by` (DCO).
