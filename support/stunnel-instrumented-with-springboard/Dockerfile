FROM platform9systems/stunnel:instrumented
ADD stunnel.sh /usr/local/bin/
ADD stunnel.conf.template /etc/stunnel/
ADD springboard /usr/local/bin/
RUN apt-get -y update && apt-get -y install gettext-base
ENTRYPOINT ["/usr/local/bin/springboard", "/bin/bash", "/usr/local/bin/stunnel.sh"]
