FROM alpine:3.5
RUN apk update --no-cache && apk add ca-certificates
COPY efs-provisioner /
ENTRYPOINT ["/efs-provisioner"]
