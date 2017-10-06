FROM debian
RUN apt-get -y update && apt-get -y install gettext stunnel
ADD stunnel.sh /usr/local/bin/
ADD stunnel.conf.template /etc/stunnel/
ENTRYPOINT ["/usr/local/bin/stunnel.sh"]
