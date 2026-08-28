# Embedded Dolt requires CGO and ICU4C headers. There is no ICU-free build tag:
# dolthub/go-icu-regex binds ICU4C unconditionally, so an unset include path is a
# build break ("unicode/regex.h file not found"), never a silent fallback.
#
# macOS: brew install icu4c.  Linux: libicu-dev, already on the default search path.
# Cross-compilation is unavailable — build on a native runner.

BIN := ./bdc

ifeq ($(shell uname -s),Darwin)
ICU_PREFIX := $(shell brew --prefix icu4c)
export CGO_CPPFLAGS := -I$(ICU_PREFIX)/include
export CGO_LDFLAGS  := -L$(ICU_PREFIX)/lib
endif
export CGO_ENABLED := 1

.PHONY: build vet test check binary clean

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./...

check: build vet test

# A local binary for smoke testing. Never install bdc globally.
binary:
	go build -o $(BIN) ./cmd/bdc

clean:
	rm -f $(BIN)
