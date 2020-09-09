FROM ubuntu:focal

MAINTAINER Davi Gupta <davigupta@gmail.com>

RUN apt-get update -y && apt-get install git python3 python3-pip psmisc net-tools curl telnet iproute2 tcpdump -y
RUN mkdir -p /tmp/nextensio
RUN mkdir -p /tmp/nextensio/test
COPY files/test/* /tmp/nextensio/test/
COPY files/src/* /tmp/nextensio/
RUN cd /tmp/nextensio \
    && pip3 install -r requirements.txt \
    && python3 -m compileall minion.py \
    && python3 -m compileall deregister.py \
    && python3 -m compileall myparse.py \
    && python3 -m compileall aaa.py \
    && mv __pycache__/minion.cpython-*.pyc minion.pyc \
    && mv __pycache__/deregister.cpython-*.pyc deregister \
    && mv __pycache__/myparse.cpython-*.pyc myparse.pyc \
    && mv __pycache__/aaa.cpython-*.pyc aaa.pyc \
    && chmod 755 minion.pyc deregister \
    && \rm -r -f __pycache__ \
    && \rm -r -f minion.py \
    && \rm -r -f deregister.py \
    && \rm -r -f myparse.py \
    && \rm -r -f aaa.py

EXPOSE 80/tcp 8002/tcp

WORKDIR /tmp/nextensio
CMD ["python3", "./minion.pyc", "-t", "--consul_dns", "--register", "--ip", "127.0.0.1", "--iport", "80"]
