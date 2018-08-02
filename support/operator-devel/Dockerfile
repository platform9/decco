FROM debian

ADD build/bin/decco-operator /usr/local/bin/
RUN apt-get -y update
RUN apt-get -y install ca-certificates
CMD while true ; do echo idling... ; sleep 5; done

