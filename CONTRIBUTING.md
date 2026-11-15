# Contributing

Nusapeta Route is an OSM router for Indonesia: one memory-mapped graph, one
binary, no database. Small contributions matter, especially wrong-route reports
and data fixes.

## Before changing code

1. Read [README.md](README.md), [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md),
   and the document related to your change.
2. Check [docs/BACKLOG.md](docs/BACKLOG.md) for known work.
3. HTTP contract or `.graph` format changes need an issue discussion first.
4. Do not edit `paper/RESULTS.md` or `paper/COMPARISON.md`; those are
   measurement snapshots. Documentation fixes belong in other files.

## Setup

Requires Go 1.27+ and `make`.

```bash
git clone https://github.com/nusapeta/route.git
cd route
make build
make java                  # download the Java PBF + build data/java.graph
bin/nusa-serve -graph data/java.graph -addr 127.0.0.1:8080
```

Commands used by CI:

```bash
gofmt -l ./cmd ./internal
go vet ./...
go test -race ./...
go build ./...
go mod tidy -diff
```

Do not add a dependency without a reason. Graphs and PBFs never enter git.

## Number rules

Every pull request that changes routing behavior MUST include:

- the `from`/`to` pair under test;
- output from `bin/nusa-graphinfo -graph ...` (format version, node/edge
  counts, PBF hash) so reviewers know which graph was used;
- for performance claims: machine, commit, and reproduction command.

Numbers without a machine, commit, and command are rejected. Accuracy claims
follow [paper/COMPARISON.md](paper/COMPARISON.md): never cite the E1 gate as
passed.

## Code

- `gofmt` is required; do not reformat files you did not touch.
- Every behavior bug needs a regression test.
- `unsafe` is limited to mmap and fixed-size record reads. Every `unsafe` block
  needs a comment explaining why it is sound.
- Do not add a new abstraction for a single caller.
- Do not commit private domains, LAN IPs, or tokens. Use
  `route.example.com` and `127.0.0.1` in examples.

## Pull request

- [ ] `gofmt`, `go vet`, `go test -race`, and `go build` pass.
- [ ] Routing behavior changes include the test pair + `nusa-graphinfo`.
- [ ] Performance claims include machine, commit, and command.
- [ ] API or format documentation is updated when the contract changes.
- [ ] No PBF, graph, index, or large data file is included.

Use imperative commit messages that name the area, for example
`serve: reject coordinates outside graph bounds`.

## DCO

The repository uses Developer Certificate of Origin 1.1, with no CLA. Every
commit MUST include a sign-off line:

```text
Signed-off-by: Your Name <you@example.com>
```

Add it with `git commit -s`. Contributions without a sign-off are not merged.

## License

By submitting a contribution, you agree to license it under MIT. OSM data and
derived graphs remain ODbL 1.0; see [LICENSE-DATA](LICENSE-DATA).
