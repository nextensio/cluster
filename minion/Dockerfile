FROM ubuntu

MAINTAINER Davi Gupta <davigupta@gmail.com>

RUN apt-get update -y && apt-get install git python3 python3-pip psmisc net-tools curl telnet iproute2 tcpdump -y
RUN mkdir -p /tmp/nextensio
RUN mkdir -p /tmp/nextensio/test
COPY files/test/* /tmp/nextensio/test/
COPY files/minion.py /tmp/nextensio
COPY files/requirements.txt /tmp/nextensio
COPY files/service.json /tmp/nextensio
COPY files/deregister.py /tmp/nextensio
COPY files/myparse.py /tmp/nextensio
COPY files/version /tmp/nextensio
RUN cd /tmp/nextensio \
    && pip3 install -r requirements.txt \
    && python3 -m compileall minion.py \
    && python3 -m compileall deregister.py \
    && python3 -m compileall myparse.py \
    && mv __pycache__/minion.cpython-36.pyc minion.pyc \
    && mv __pycache__/deregister.cpython-36.pyc deregister \
    && mv __pycache__/myparse.cpython-36.pyc myparse.pyc \
    && chmod 755 minion.pyc deregister \
    && \rm -r -f __pycache__ \
    && \rm -r -f minion.py \
    && \rm -r -f deregister.py \
    && \rm -r -f myparse.py

EXPOSE 80/tcp 8002/tcp

WORKDIR /tmp/nextensio
CMD ["python3", "./minion.pyc", "-t", "--consul_dns", "--register", "--ip", "127.0.0.1", "--iport", "80"]

