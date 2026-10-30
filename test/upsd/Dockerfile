# A real upsd for the integration test (test/upsd/upsd_test.go): Debian's nut-server with the
# dummy-ups driver on one UPS "dummy", whose variables the test sets with upsrw. STARTTLS with a
# self-signed certificate when this build of upsd has SSL; a user "admin" that may SET and a
# read-only "monuser".
FROM debian:trixie-slim
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nut-server nut-client openssl \
 && rm -rf /var/lib/apt/lists/*
COPY nut.conf ups.conf upsd.conf upsd.users dummy.dev /etc/nut/
COPY start.sh /start.sh
RUN chmod 640 /etc/nut/upsd.conf /etc/nut/upsd.users && chown root:nut /etc/nut/* && chmod +x /start.sh
EXPOSE 3493
CMD ["/start.sh"]
