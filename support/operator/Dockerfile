FROM debian:latest

ADD decco-operator /usr/local/bin/

RUN apt-get -y update
RUN apt-get -y install ca-certificates

ENTRYPOINT ["/usr/local/bin/decco-operator"]
