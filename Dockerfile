FROM golang:1.22 AS build

WORKDIR /build

COPY . .

RUN GOEXPERIMENT=boringcrypto go build


FROM cgr.dev/chainguard/glibc-dynamic

COPY --from=build /build/sfkit-proxy /

ENTRYPOINT [ "/sfkit-proxy" ]
