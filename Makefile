IMAGE ?= gavinmcnair/tvproxy-streams
TAG ?= latest
ARCH := $(shell uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')

build:
	go build -o tvproxy-streams ./cmd/tvproxy-streams/

test:
	go test ./...

docker-build:
	docker build -t $(IMAGE):$(TAG) .

runner-build:
	docker build -t $(IMAGE):$(TAG)-$(ARCH) .
	docker push $(IMAGE):$(TAG)-$(ARCH)

.PHONY: build test docker-build runner-build
