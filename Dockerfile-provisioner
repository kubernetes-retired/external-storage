FROM golang:latest AS golang
WORKDIR /go/src/github.com/kubernetes-incubator/external-storage
RUN go get -u github.com/golang/dep/cmd/dep
COPY . .
RUN dep ensure
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o snapshotprovisioner snapshot/cmd/snapshot-pv-provisioner/snapshot-pv-provisioner.go

FROM scratch
COPY --from=golang /go/src/github.com/kubernetes-incubator/external-storage/snapshotprovisioner /
COPY --from=golang /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/snapshotprovisioner"]
