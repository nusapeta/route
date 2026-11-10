# routeid - build, test, and data targets.
GO      ?= go
REGION  ?= java
PROFILE ?= car
MEMLIM  ?= 2GiB
# Regions Geofabrik publishes for Indonesia (see scripts/fetch.sh).
ISLANDS  = java sumatra kalimantan sulawesi nusa-tenggara maluku papua

# Provenance stamped into binaries: `make build` records the commit, `make
# static` uses a reproducible SOURCE_DATE_EPOCH when the build must be bit-identical.
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
BUILT   := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.commit=$(COMMIT) -X main.buildTime=$(BUILT) \
           -X routeid/internal/extract.Version=$(COMMIT)
STATIC  := -s -w $(LDFLAGS)

.PHONY: all build test vet fmt clean fetch graph java sumatra islands motor bench restcheck refresh

all: build

build:
	$(GO) build -ldflags '$(LDFLAGS)' -o bin/ ./cmd/...

# binary statis (CGO_ENABLED=0) untuk image alpine/distroless
static:
	mkdir -p bin/static
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/rid-server ./cmd/rid-server
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/rid-gateway ./cmd/rid-gateway
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/rid-extract ./cmd/rid-extract
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/rid-route ./cmd/rid-route
	CGO_ENABLED=0 go build -trimpath -ldflags='$(STATIC)' -o bin/static/rid-graphinfo ./cmd/rid-graphinfo

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
# The car profile keeps the historical name (data/java.graph); other profiles
# get a suffix so an A/B stays one command away.
graph: build
	[ -f data/$(REGION)-latest.osm.pbf ] || scripts/fetch.sh $(REGION)
	GOMEMLIMIT=$(MEMLIM) bin/rid-extract -pbf data/$(REGION)-latest.osm.pbf \
	  -out data/$(REGION)$(if $(filter-out car,$(PROFILE)),-$(PROFILE),).graph \
	  -profile $(PROFILE)

java:
	$(MAKE) graph REGION=java

sumatra:
	$(MAKE) graph REGION=sumatra

# Motorcycle graphs for every island, next to the car ones.
islands: build
	@for r in $(ISLANDS); do $(MAKE) graph REGION=$$r PROFILE=car || exit 1; done

motor: build
	@for r in $(ISLANDS); do $(MAKE) graph REGION=$$r PROFILE=motor || exit 1; done

bench: build
	bin/rid-bench -graph data/java.graph -pairs paper/results/pairs-java.csv

docker: build
	cd deploy && docker compose build

restcheck: build
	bin/rid-restcheck -graph data/java.graph -json paper/restrcheck-java.json

# One command for a monthly data refresh: download, rebuild car and motor,
# census the ways, run the golden suite, and do a short A/B on localhost.
refresh: build
	scripts/fetch.sh $(REGION)
	$(MAKE) graph REGION=$(REGION) PROFILE=car
	$(MAKE) graph REGION=$(REGION) PROFILE=motor
	bin/rid-tagstats -pbf data/$(REGION)-latest.osm.pbf > data/$(REGION)-tagstats.txt
	bin/rid-graphinfo -graph data/$(REGION).graph > data/$(REGION)-graphinfo.txt
	bin/rid-graphinfo -graph data/$(REGION)-motor.graph >> data/$(REGION)-graphinfo.txt
	$(MAKE) test
	scripts/refresh-ab.sh $(REGION)
