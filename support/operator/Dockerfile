FROM debian

ADD decco-operator /usr/local/bin/
RUN apt-get -y update
RUN apt-get -y install ca-certificates
CMD ["decco-operator"]
