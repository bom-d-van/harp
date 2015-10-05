FROM ubuntu:latest
MAINTAINER Sven Dowideit <SvenDowideit@docker.com>

RUN apt-get update && apt-get install -y openssh-server
RUN mkdir /var/run/sshd
RUN echo 'root:root' | chpasswd
RUN sed -i 's/PermitRootLogin without-password/PermitRootLogin yes/' /etc/ssh/sshd_config

# SSH login fix. Otherwise user is kicked off after login
RUN sed 's@session\s*required\s*pam_loginuid.so@session optional pam_loginuid.so@g' -i /etc/pam.d/sshd

ENV NOTVISIBLE "in users profile"
RUN echo "export VISIBLE=now" >> /etc/profile

RUN locale-gen en_US.UTF-8

RUN adduser --disabled-password --gecos "" app
RUN ssh-keygen -q -t rsa -N '' -f /id_rsa
RUN apt-get install rsync

# TODO: generate ssh keys

EXPOSE 22
CMD ["/usr/sbin/sshd", "-D"]
