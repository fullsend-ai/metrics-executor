FROM registry.access.redhat.com/ubi9/go-toolset:1.26.10@sha256:c2bd0309bf78ae317024fdfc5602a31704e0dba9572f6d4b0bd4b681033dc965 AS builder
WORKDIR /opt/app-root/src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -o out/metrics-executor ./cmd/metrics-executor/

FROM registry.access.redhat.com/ubi9/ubi-minimal:9.8@sha256:4b79c93106c14ad1c234907f83441ff0e4f8d24e78426a36e159098e9f7e6fa5 AS workflows
RUN microdnf install -y ca-certificates && microdnf clean all
COPY --from=builder /opt/app-root/src/out/metrics-executor /metrics-executor
EXPOSE 7070
USER 1001
ENTRYPOINT ["/metrics-executor"]
