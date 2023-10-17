FROM golang:1.21 AS build

WORKDIR /build

COPY . .

RUN CGO_ENABLED=0 go build


FROM scratch

COPY --from=build /build/sfkit-proxy /

ENTRYPOINT [ "/sfkit-proxy" ]
