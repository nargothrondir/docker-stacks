# Two stages. The first has a Go toolchain, the second has nothing but the
# binary — so the compiler never reaches a node, and the image that holds the
# Cloudflare token is not also carrying a package manager and a shell.
#
# Build locally with:  docker build -t acme-hook remnanode/acme-hook
# CI does the same and publishes to ghcr.io — see .github/workflows/acme-hook.yml

FROM golang:1.23-alpine AS build

WORKDIR /src

# There are no dependencies to download, so there is no dependency layer worth
# caching separately: the module file and the source go in together.
COPY go.mod main.go ./

# CGO_ENABLED=0 produces a static binary, which is what lets the final stage be
# an image with no libc in it at all. -trimpath keeps build paths out of the
# binary, and -s -w drop the symbol table and DWARF info.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /acme-hook .

# distroless/static carries CA certificates and essentially nothing else: no
# shell, no package manager, no interpreter. The certificates are the reason it
# is not FROM scratch — the hook talks to api.cloudflare.com over HTTPS, and an
# empty image fails certificate verification on the first call.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /acme-hook /acme-hook

# The image already defaults to uid 65532; stated here so it is visible rather
# than inherited. The hook binds 127.0.0.1:9001, an unprivileged port, so it
# needs nothing more than that.
USER nonroot:nonroot

# The compose healthcheck runs `/acme-hook -healthcheck`, because there is no
# shell or interpreter in here to probe the port with.
ENTRYPOINT ["/acme-hook"]
