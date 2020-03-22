FROM __STUNNEL_CONTAINER_TAG__
ADD stunnel.sh /usr/local/bin/
ADD stunnel.conf.template /etc/stunnel/
ADD springboard /usr/local/bin/
RUN apt-get -y update && apt-get -y install gettext-base


# The stunnel container is equipped with a system-wide libkeepalive, which
# enables TCP keepalive for all sockets by default. It allows the following
# systctl parameters to be configured via environment variables:
#   env               sysctl
#   KEEPCNT     <=>   net.ipv4.tcp_keepalive_probes
#   KEEPIDLE    <=>   net.ipv4.tcp_keepalive_time
#   KEEPINTVL   <=>   net.ipv4.tcp_keepalive_intvl
# Reduce the idle period (net.ipv4.tcp_keepalive_time) to 75 seconds, since
# the Linux system default of 7200, or 2 hours, is too long to be useful.
ENV KEEPIDLE 75

ENTRYPOINT ["/usr/local/bin/springboard", "/bin/bash", "/usr/local/bin/stunnel.sh"]
