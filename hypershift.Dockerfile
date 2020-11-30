FROM registry.svc.ci.openshift.org/openshift/release:golang-1.15 as builder
WORKDIR /workspace

# Copy the sources
COPY ./ ./

RUN go mod vendor

RUN go build .

# Build
ARG package=.
ARG ARCH
ARG ldflags

# Do not force rebuild of up-to-date packages (do not use -a) and use the compiler cache folder
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} \
    go build -ldflags "${ldflags} -extldflags '-static'" \
    -o manager ${package}

FROM quay.io/openshift/origin-base:4.6
WORKDIR /
COPY --from=builder /workspace/manager .
USER nobody
ENTRYPOINT ["/manager"]
