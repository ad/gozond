IMG = github.com/ad/gozond
DEV-TAG = dev
TAG = latest
CWD = $(shell pwd)

build: #test 
	@docker build -t $(IMG):$(TAG) .

devbuild: #test 
	@docker build -t $(IMG):$(DEV-TAG) . -f Dockerfile-dev

devup:
	@docker-compose -f docker-compose.dev.yml up

test:
	@docker run --rm -v $(CWD):$(CWD) -w $(CWD) golang:alpine sh -c "CGO_ENABLED=0 go test -mod=vendor  -v"

clean:
	@docker-compose -f docker-compose.dev.yml rm -sfv

dev: devbuild devup

.PHONY: build
