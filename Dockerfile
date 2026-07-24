# --- Build stage ---
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Cache module downloads separately from source changes
COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# CGO disabled + static build so the binary runs standalone in the
# minimal final image below (no glibc/musl dependency issues).
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/ug-chords-backend .

# --- Final stage ---
FROM gcr.io/distroless/static-debian12

COPY --from=builder /out/ug-chords-backend /ug-chords-backend

# Cloud Run sets PORT itself and expects the container to listen on it;
# EXPOSE here is documentation only (Cloud Run ignores it and injects
# PORT via env var, which main.go now reads).
EXPOSE 8080

ENTRYPOINT ["/ug-chords-backend"]