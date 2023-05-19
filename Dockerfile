# 编译
FROM ghcr.io/hongfs/env:golang120 as build

ENV GO111MODULE=on \
    CGO_ENABLED=0

WORKDIR /code

COPY . .

RUN go mod tidy && \
    env GOOS=linux GOARCH=amd64 go build -o main main.go

FROM ghcr.io/hongfs/env:alpine

WORKDIR /build

COPY --from=build /code/main /build/

EXPOSE 53/udp

CMD ["./main"]