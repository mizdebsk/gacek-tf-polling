FROM golang:1.22 as build
ENV CGO_ENABLED=0
COPY *.go /
RUN ["go","build","-o","/app","/polling.go"]

FROM gcr.io/distroless/static:nonroot
COPY --from=build /app /
CMD ["/app"]
