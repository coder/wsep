FROM golang:1

ENV GOFLAGS="-mod=readonly"
ENV CI=true

RUN apt update && apt install -y screen

RUN go install golang.org/x/tools/cmd/goimports@latest
RUN go install golang.org/x/lint/golint@latest
RUN go install github.com/mattn/goveralls@latest
