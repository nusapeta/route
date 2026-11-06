# routeid — build, test, and data targets.
GO      ?= go
REGION  ?= java
MEMLIM  ?= 2GiB

.PHONY: all build test vet fmt clean fetch java sumatra bench restcheck

all: build

build:
	$(GO) build -o bin/ ./cmd/...

# binary statis (CGO_ENABLED=0) untuk image alpine/distroless
static:
	mkdir -p bin/static
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/static/rid-server ./cmd/rid-server
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/static/rid-extract ./cmd/rid-extract
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/static/rid-route ./cmd/rid-route
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/static/rid-graphinfo ./cmd/rid-graphinfo

test: vet
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w ./cmd ./internal

clean:
	rm -rf bin

fetch:
	mkdir -p data
	scripts/fetch.sh $(REGION)

# Build a regional graph. Extraction is the only memory-hungry step, so cap
# the GC: Java peaks at ~1.8 GB with this limit instead of ~3.2 GB.
java: build
	[ -f data/java-latest.osm.pbf ] || scripts/fetch.sh java
	GOMEMLIMIT=$(MEMLIM) bin/rid-extract -pbf data/java-latest.osm.pbf -out data/java.graph

sumatra: build
	[ -f data/sumatra-latest.osm.pbf ] || scripts/fetch.sh sumatra
	GOMEMLIMIT=$(MEMLIM) bin/rid-extract -pbf data/sumatra-latest.osm.pbf -out data/sumatra.graph

bench: build
	bin/rid-bench -graph data/java.graph -pairs paper/results/pairs-java.csv

docker: build
	cd deploy && docker compose build

restcheck: build
	bin/rid-restcheck -graph data/java.graph -json paper/restrcheck-java.json
