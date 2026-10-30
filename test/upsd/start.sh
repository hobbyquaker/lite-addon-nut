#!/bin/sh
# a certificate for STARTTLS, offered through CERTFILE when this build of upsd knows the directive
# (Debian trixie's 2.8.1 does not: "invalid directive CERTFILE", and STARTTLS answers
# FEATURE-NOT-CONFIGURED, which the test accepts and skips)
if [ ! -f /etc/nut/upsd.pem ]; then
    openssl req -x509 -newkey rsa:2048 -nodes -days 2 -subj '/CN=upsd.test' -addext 'subjectAltName=DNS:upsd.test,IP:127.0.0.1' \
        -keyout /tmp/key.pem -out /tmp/cert.pem >/dev/null 2>&1
    cat /tmp/key.pem /tmp/cert.pem > /etc/nut/upsd.pem
    chown root:nut /etc/nut/upsd.pem && chmod 640 /etc/nut/upsd.pem
    cp /tmp/cert.pem /etc/nut/ca.pem && chmod 644 /etc/nut/ca.pem
fi
if upsd -h 2>&1 | grep -qi ssl; then echo "CERTFILE /etc/nut/upsd.pem" >> /etc/nut/upsd.conf; fi
mkdir -p /run/nut && chown nut:nut /run/nut
upsdrvctl -u nut start || exit 1
exec upsd -u nut -D
