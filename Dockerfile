# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/disillusioned-labs/audit/internal/app.version=${VERSION} \
        -X github.com/disillusioned-labs/audit/internal/app.commit=${COMMIT} \
        -X github.com/disillusioned-labs/audit/internal/app.buildDate=${BUILD_DATE}" \
      -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/disillusioned-labs/audit/internal/app.version=${VERSION} \
        -X github.com/disillusioned-labs/audit/internal/app.commit=${COMMIT} \
        -X github.com/disillusioned-labs/audit/internal/app.buildDate=${BUILD_DATE}" \
      -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/disillusioned-labs/audit/internal/app.version=${VERSION} \
        -X github.com/disillusioned-labs/audit/internal/app.commit=${COMMIT} \
        -X github.com/disillusioned-labs/audit/internal/app.buildDate=${BUILD_DATE}" \
      -o /out/generate-signing-key ./cmd/generate-signing-key


# ---------------------------------------------------------------------------
# Runtime image
# ---------------------------------------------------------------------------

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app

COPY --from=build /out/api ./api
COPY --from=build /out/worker ./worker

EXPOSE 8080

ENTRYPOINT ["/app/api"]


# ---------------------------------------------------------------------------
# Signing-key provisioning image
# ---------------------------------------------------------------------------

FROM gcr.io/distroless/static-debian12:nonroot AS signing-key

WORKDIR /app

COPY --from=build /out/generate-signing-key ./generate-signing-key

ENTRYPOINT ["/app/generate-signing-key"]