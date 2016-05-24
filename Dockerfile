FROM golang:1.5.1

ADD ./cuberite_server /srv/cuberite_server
ADD ./world /srv/world
ADD ./docs/img/logo64x64.png /srv/logo.png
ADD ./start.sh /srv/start.sh
ADD ./go/src/kubeproxy/kubeproxy /bin/goproxy
ADD ./docker_linux_x64/docker /bin/docker

RUN wget https://storage.googleapis.com/kubernetes-release/release/v1.0.6/bin/linux/amd64/kubectl
RUN mv kubectl /bin/kubectl
RUN chmod +x /bin/kubectl

# Download Cuberite server (Minecraft C++ server)
# and load up a special empty world for Dockercraft
WORKDIR /srv
RUN sh -c "$(wget -qO - https://raw.githubusercontent.com/cuberite/cuberite/master/easyinstall.sh)" && mv Server cuberite_server
COPY ./world world
COPY ./docs/img/logo64x64.png logo.png
COPY ./start.sh start.sh
CMD ["/bin/bash","/srv/start.sh"]
