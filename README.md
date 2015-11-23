# Kubecraft

A simple Minecraft Docker client, to visualize and manage Kubernetes pods.

[YouTube video](https://youtu.be/A4qwsSEldHE)

1. **Install Minecraft: [minecraft.net](https://minecraft.net)**

	The Minecraft client hasn't been modified, just get the official release.

2. **Pull or build Kubecraft image:**

	```
	docker pull stevesloka/kubecraft
	```
	or

	```
	git clone git@github.com:stevesloka/kubecraft.git
	cd kubecraft && go/src/kubeproxy/make
	docker build -t stevesloka/kubecraft .
	```
3. **Run Kubecraft container:**

	```
	docker run -t -d -i -p 25565:25565 \
	--name kubecraft \
	-e KUBE_CFG_FILE=/etc/kubeconfig \
	-v ~/.kube/config:/etc/kubeconfig \
	stevesloka/kubecraft
	```

	Copying `kubeconfig` file to enable k8s api server

	The default port for a Minecraft server is *25565*, if you prefer a different one: `-p <port>:25565`

4. **Open Minecraft > Multiplayer > Add Server**

	The server address is the IP of Docker host. No need to specify a port if you used the default one.

	If you're using [Docker Machine](https://docs.docker.com/machine/install-machine/): `docker-machine ip <machine_name>`

5. **Join Server!**

	You should see at least one container in your world, which is the one hosting your Kubecraft server.

	You can start, stop and remove containers interacting with levers and buttons. Some Docker commands are also supported directly via Minecraft's chat window, which is displayed by pressing the `T` key (default) or `/` key.

> A command always starts with a `/`.
>
> If you open the prompt using the `/` key, it will be prefilled with a `/` character, but if you open it with the `T` key, it will not be prefilled and you will have to type a `/` yourself before typing your docker command.
>
> example: `/docker run redis`.

## How it works

The Minecraft client itself remains unmodified. All operations are done server side.

The Minecraft server we use is [http://cuberite.org](http://cuberite.org). A custom Minecraft compatible game server written in C++. [github repo](https://github.com/cuberite/cuberite)

This server accepts plugins, scripts written in Lua. So we did one for Docker. (world/Plugins/Docker)

Unfortunately, there's no nice API to communicate with these plugins. But there's a webadmin, and plugins can be responsible for "webtabs".

```lua
Plugin:AddWebTab("Docker",HandleRequest_Docker)
```

Basically it means the plugin can catch POST requests sent to `http://127.0.0.1:8080/webadmin/Docker/Docker`.

### Kubeproxy

Events from the Kubernetes API are transmitted to the Lua plugin by a small daemon (written in Go). (go/src/kubeproxy)

```go
func MCServerRequest(data url.Values, client *http.Client) {
	req, _ := http.NewRequest("POST", "http://127.0.0.1:8080/webadmin/Docker/Docker", strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("admin", "admin")
	client.Do(req)
}
```

The kubeproxy binary can also be executed with parameters from the Lua plugin, to send requests to the daemon:

```lua
function PlayerJoined(Player)
	-- refresh containers
	r = os.execute("goproxy containers")
end
```
## Thanks!

Thanks to the awesome folks who built Dockercraft! Please go check it out as well: https://github.com/docker/dockercraft

If you're interested about Dockercraft's design, discussions happen in [that issue](https://github.com/docker/dockercraft/issues/19).
Also, we're using [Magicavoxel](https://voxel.codeplex.com) to do these nice prototypes:

You can find our Magicavoxel patterns in [that folder](![Dockercraft](https://github.com/docker/dockercraft/tree/master/docs/magicavoxel)).

To get fresh news, follow their Twitter account: [@dockercraft](https://twitter.com/dockercraft).
