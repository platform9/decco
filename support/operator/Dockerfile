FROM alpine:3.16

ADD decco-operator /usr/local/bin/
RUN apk update
RUN apk add --no-cache bash
RUN apk add --no-cache libc6-compat
RUN apk add curl
RUN apk add ca-certificates
CMD ["decco-operator"]
