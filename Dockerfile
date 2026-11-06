# Stage 1: build frontend static assets
FROM node:20-alpine AS frontend-build
WORKDIR /app
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci
COPY frontend/ .
RUN npm run build

# Stage 2: build backend binary
FROM golang:1.25-alpine AS backend-build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X github.com/percona/obs-dashboard/internal/version.Version=${VERSION}" -o /obsboard ./cmd/obsboard

# Stage 3: minimal runtime image
FROM ghcr.io/aquasecurity/trivy:latest AS trivy

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=trivy /usr/local/bin/trivy /usr/local/bin/trivy
COPY --from=backend-build /obsboard /obsboard
COPY --from=frontend-build /app/dist /frontend
ENV FRONTEND_DIR=/frontend
EXPOSE 4000
ENTRYPOINT ["/obsboard"]
