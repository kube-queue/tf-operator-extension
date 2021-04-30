FROM golang:1.15.3-alpine3.12 as build
RUN apk add make git
ADD . /go/src/github.com/kube-queue/tf-operator-extension
WORKDIR /go/src/github.com/kube-queue/tf-operator-extension
RUN make

FROM alpine:3.12
COPY --from=build /go/src/github.com/kube-queue/tf-operator-extension/bin/tf-operator-extension /usr/bin/tf-operator-extension
RUN chmod +x /usr/bin/tf-operator-extension
ENTRYPOINT ["tf-operator-extension"]