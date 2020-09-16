# minion
Humble workers

To test on local machine

Step1: run minion
    docker run -it --net host \
           -e MY_NODE_NAME='k8s-worker1' \
           -e MY_POD_NAME='tom.com' \
           -e MY_POD_NAMESPACE='default' \
           -e MY_POD_IP='127.0.0.1' \
           -e MY_POD_CLUSTER='sjc' \
           -e MY_DNS='157.230.160.64' \
           -e MY_SIM_TEST='T' \
           -e MY_MONGO_URI='mongodb+srv//:127.0.0.1' \
           davigupta/minion:0.70 python3 /tmp/nextensio/minion.pyc -t

Step2: run test client in files/test/shorty.py
    cd files/test; ./shorty.py
   

Run output

1
['service-1']
ws://127.0.0.1:8002
< Hello NCTR!
hello
> b'GET / HTTP/1.1\r\nx-nextensio-attr: { dept: computer-science, team: blue }\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: ./shorty.py\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 23\r\n\r\n<body>\r\nhello\n</body>\r\n'
tello
> b'GET / HTTP/1.1\r\nx-nextensio-attr: { dept: computer-science, team: blue }\r\nHost: gateway.sjc.nextensio.net\r\nuser-agent: ./shorty.py\r\nx-nextensio-for: 127.0.0.1\r\nx-nextensio-uuid: 12345678\r\ncontent-length: 23\r\n\r\n<body>\r\ntello\n</body>\r\n'
^Csnd_count 2, rcv_count 2
^Csnd_count 2, rcv_count 2
