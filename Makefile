# Copyright 2020 Datawire. All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

TELEPRESENCE_REGISTRY ?= docker.io/datawire
DOCKER_PUSH           ?= docker-push

# When building a release, or if you don't want constant rebuilds, use
#   export VERSION_SUFFIX=
VERSION_SUFFIX        ?= -$(TIME)

_TIME := $(shell date +%s)
TELEPRESENCE_VERSION ?= $(shell git describe --tags)$(foreach TIME,$(_TIME),$(VERSION_SUFFIX))

default: help
.PHONY: default
.SECONDARY:
.DELETE_ON_ERROR:

include build/tools.mk
include build/go.mk
include build/support.mk

.PHONY: prepare-release
prepare-release: ## (Release) Update nescessary files and tag the release (does not push)
	go mod edit -require=github.com/datawire/telepresence2/rpc/v2@$(TELEPRESENCE_VERSION)
	git add go.mod
	git commit --message='Prepare $(TELEPRESENCE_VERSION)'
	git tag --annotate --message='$(TELEPRESENCE_VERSION)' $(TELEPRESENCE_VERSION)
	git tag --annotate --message='$(TELEPRESENCE_VERSION)' rpc/$(TELEPRESENCE_VERSION)
