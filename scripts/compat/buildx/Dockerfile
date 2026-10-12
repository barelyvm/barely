FROM golang:1.23-alpine AS build
WORKDIR /src
COPY main.go .
RUN --mount=type=cache,target=/root/.cache/go-build go mod init x && go build -o /out/app .
FROM alpine
COPY --from=build /out/app /app
CMD ["/app"]
