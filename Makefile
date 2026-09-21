REG  ?= localhost:5001
REPO ?= hello
TAG  ?= v1
ARCH ?= arm64

.PHONY: all image push explain verify registry down clean

all: image push verify

out/hello: payload/main.go
	mkdir -p out
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -buildvcs=false -ldflags="-s -w" -o out/hello ./payload

image: out/hello
	go run . build -bin out/hello -out out/image -arch $(ARCH)

push: image
	go run . push -out out/image -registry http://$(REG) -repo $(REPO) -tag $(TAG)

explain: image
	go run . explain -out out/image

# 5000 is AirPlay Receiver on macOS, hence 5001.
registry:
	colima status >/dev/null 2>&1 || colima start
	docker rm -f oci-registry >/dev/null 2>&1 || true
	docker run -d --name oci-registry -p 5001:5000 registry:2 >/dev/null
	echo "registry listening on $(REG)"

verify:
	docker rmi $(REG)/$(REPO):$(TAG) >/dev/null 2>&1 || true
	docker run --rm --platform linux/$(ARCH) $(REG)/$(REPO):$(TAG)

down:
	docker rm -f oci-registry >/dev/null 2>&1 || true

clean:
	rm -rf out
