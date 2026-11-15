# nusapeta-route - build, test, and data targets.
GO      ?= go
REGION  ?= java
MEMLIM  ?= 2GiB
# Regions Geofabrik publishes for Indonesia (see scripts/fetch.sh).
ISLANDS  = java sumatra kalimantan sulawesi nusa-tenggara maluku papua

# Provenance stamped into binaries: `make build` records the commit, `make
# static` uses a reproducible SOURCE_DATE_EPOCH when the build must be bit-identical.
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
BUILT   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.commit=$(COMMIT) -X main.buildTime=$(BUILT) \
           -X github.com/nusapeta/route/internal/extract.Version=$(COMMIT)
STATIC  := -s -w $(LDFLAGS)

.PHONY: all build test vet fmt clean fetch graph java sumatra islands bench restcheck refresh

all: build

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...

# binary statis (CGO_ENABLED=0) untuk image alpine/distroless
static:
	mkdir -p bin/static
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/nusa-serve ./cmd/nusa-serve
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/nusa-gateway ./cmd/nusa-gateway
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/nusa-extract ./cmd/nusa-extract
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/nusa-route ./cmd/nusa-route
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/nusa-graphinfo ./cmd/nusa-graphinfo

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
# One artefact carries both the car and the motorcycle profile (v5).
graph: build
	[ -f data/$(REGION)-latest.osm.pbf ] || scripts/fetch.sh $(REGION)
	GOMEMLIMIT=$(MEMLIM) bin/nusa-extract -pbf data/$(REGION)-latest.osm.pbf \
	  -out data/$(REGION).graph

java:
	$(MAKE) graph REGION=java

sumatra:
	$(MAKE) graph REGION=sumatra

# One union graph per island.
islands: build
	@for r in $(ISLANDS); do $(MAKE) graph REGION=$$r || exit 1; done

bench: build
	bin/nusa-bench -graph data/java.graph -pairs paper/results/pairs-java.csv

docker: build
	cd deploy && docker compose build

restcheck: build
	bin/nusa-restcheck -graph data/java.graph -json paper/restrcheck-java.json

# One command for a monthly data refresh: download, rebuild, census the ways,
# run the golden suite, and do a short A/B on localhost.
refresh: build
	scripts/fetch.sh $(REGION)
	$(MAKE) graph REGION=$(REGION)
	bin/nusa-tagstats -pbf data/$(REGION)-latest.osm.pbf > data/$(REGION)-tagstats.txt
	bin/nusa-graphinfo -graph data/$(REGION).graph > data/$(REGION)-graphinfo.txt
	$(MAKE) test
	scripts/refresh-ab.sh $(REGION)
