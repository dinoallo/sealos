FROM golang:1.24-alpine AS build

WORKDIR /src
RUN printf 'module github.com/labring/sealos/controllers/user\n\ngo 1.24\n' > go.mod
COPY test/oidc/server.go ./test/oidc/server.go
COPY test/oidc/cmd/test-oidc/main.go ./test/oidc/cmd/test-oidc/main.go
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/test-oidc ./test/oidc/cmd/test-oidc

FROM scratch
COPY --from=build /out/test-oidc /test-oidc
EXPOSE 8080
ENTRYPOINT ["/test-oidc"]
