FROM ubuntu:14.04

MAINTAINER jinxinxin@qiniu.com

#move files
RUN mkdir -p /root/unzip/
ADD deploy/* /root/unzip/

#set env variables
EXPOSE 9100

#start service
WORKDIR /root/unzip
ENTRYPOINT ./qufop qufop.conf