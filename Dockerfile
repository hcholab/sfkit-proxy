FROM golang:1.22 AS build

WORKDIR /build

COPY . .

RUN CGO_ENABLED=0 go build


FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /build/sfkit-proxy /

ENTRYPOINT [ "/sfkit-proxy" ]
