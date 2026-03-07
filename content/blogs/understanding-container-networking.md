---
title: "Understanding Container Networking from the Ground Up"
date: 2026-02-18
tags: ["docker", "containers", "networking", "linux", "devops", "namespaces"]
description: "A detailed look at how container networking actually works under the hood — network namespaces, veth pairs, bridges, iptables, and how Docker stitches them together."
status: published
---

## The Illusion of Isolation

When you run `docker run --name web -p 8080:80 nginx`, the container gets its own IP address, its own network stack, and port 8080 on the host magically forwards to port 80 inside the container. But there's no magic here — it's all built on Linux kernel primitives that have existed for over a decade. Understanding these primitives demystifies container networking and makes debugging much easier.

In this post, I'll build a container network from scratch using raw Linux commands, then show how Docker automates exactly the same steps.

## Network Namespaces

The foundational building block is the **network namespace**. A network namespace gives a process its own isolated view of the network stack — its own interfaces, routing table, iptables rules, and sockets. Two processes in different network namespaces cannot see each other's network traffic by default.

You can create and inspect network namespaces directly:

```bash
# Create two namespaces
sudo ip netns add container1
sudo ip netns add container2

# List them
ip netns list

# Run a command inside a namespace
sudo ip netns exec container1 ip addr
```

That last command will show only a loopback interface in the `DOWN` state. The namespace starts completely empty — no connectivity to the outside world.

## Veth Pairs

To connect a namespace to anything, you need a **veth pair** — a virtual ethernet cable with two ends. Whatever goes in one end comes out the other. You place one end inside the namespace and the other end on the host (or on a bridge).

```bash
# Create a veth pair
sudo ip link add veth0 type veth peer name ceth0

# Move one end into the container namespace
sudo ip link set ceth0 netns container1

# Assign IPs
sudo ip addr add 172.18.0.1/24 dev veth0
sudo ip netns exec container1 ip addr add 172.18.0.2/24 dev ceth0

# Bring both interfaces up
sudo ip link set veth0 up
sudo ip netns exec container1 ip link set ceth0 up
sudo ip netns exec container1 ip link set lo up
```

Now you can ping the container from the host:

```bash
ping 172.18.0.2  # works
sudo ip netns exec container1 ping 172.18.0.1  # also works
```

This is exactly what Docker does when it creates a container with the default bridge network. The `vethXXXXXX` interfaces you see on the host via `ip link` are the host-side ends of these pairs.

## The Bridge

Point-to-point veth pairs work for a single container, but what if you have 20 containers that need to talk to each other? You'd need a fully meshed set of veth pairs, which is impractical. Instead, we use a **bridge** — a virtual Layer 2 switch.

```bash
# Create a bridge
sudo ip link add br0 type bridge
sudo ip link set br0 up
sudo ip addr add 172.18.0.1/24 dev br0

# Create veth pairs for two containers
sudo ip link add veth1 type veth peer name ceth1
sudo ip link add veth2 type veth peer name ceth2

# Attach host ends to the bridge
sudo ip link set veth1 master br0
sudo ip link set veth2 master br0
sudo ip link set veth1 up
sudo ip link set veth2 up

# Move container ends into namespaces and configure
sudo ip link set ceth1 netns container1
sudo ip link set ceth2 netns container2

sudo ip netns exec container1 ip addr add 172.18.0.2/24 dev ceth1
sudo ip netns exec container1 ip link set ceth1 up

sudo ip netns exec container2 ip addr add 172.18.0.3/24 dev ceth2
sudo ip netns exec container2 ip link set ceth2 up
```

Now both containers can reach each other through the bridge, and they can reach the host. The bridge is the `docker0` interface you see on any Docker host.

### Container-to-Container Communication

With the bridge in place, containers communicate at Layer 2. Frames from `container1` arrive at the bridge via `veth1`, and the bridge forwards them to `veth2` based on MAC address learning — the same way a physical switch works.

```bash
sudo ip netns exec container1 ping 172.18.0.3  # container2 reachable
sudo ip netns exec container2 ping 172.18.0.2  # container1 reachable
```

## Reaching the Outside World

Containers can talk to each other, but they can't reach the internet yet. For that, you need NAT (Network Address Translation) via iptables. The host must masquerade outgoing traffic from the container subnet:

```bash
# Enable IP forwarding
sudo sysctl -w net.ipv4.ip_forward=1

# Add a default route inside each container
sudo ip netns exec container1 ip route add default via 172.18.0.1
sudo ip netns exec container2 ip route add default via 172.18.0.1

# Masquerade outgoing traffic
sudo iptables -t nat -A POSTROUTING -s 172.18.0.0/24 ! -o br0 -j MASQUERADE
```

Now containers can reach external hosts. The kernel replaces the source address (172.18.0.x) with the host's external IP, tracks the connection in the conntrack table, and translates responses back.

## Port Forwarding

The `-p 8080:80` flag in Docker is implemented as a DNAT (Destination NAT) rule:

```bash
# Forward host port 8080 to container1 port 80
sudo iptables -t nat -A PREROUTING -p tcp --dport 8080 \
    -j DNAT --to-destination 172.18.0.2:80

# Also handle locally-generated traffic
sudo iptables -t nat -A OUTPUT -p tcp --dport 8080 \
    -j DNAT --to-destination 172.18.0.2:80

# Allow forwarded traffic
sudo iptables -A FORWARD -p tcp -d 172.18.0.2 --dport 80 -j ACCEPT
```

You can verify this on a real Docker host by inspecting the nat table:

```bash
sudo iptables -t nat -L -n -v
```

You'll see Docker's chains (`DOCKER`, `DOCKER-ISOLATION-STAGE-1`, etc.) managing exactly these kinds of rules.

## DNS Resolution

Docker runs an embedded DNS server at `127.0.0.11` inside user-defined bridge networks. When a container resolves another container's name, the request goes to this embedded server, which looks up the target container's IP from Docker's internal state. This is why `docker run --name db postgres` lets other containers on the same network reach it via the hostname `db`.

On the default bridge network, DNS resolution between containers does not work — you must use `--link` (deprecated) or switch to a user-defined network.

## Debugging Tips

When container networking breaks, these commands are your best friends:

- `ip netns exec <ns> ip addr` — Check interfaces and IPs inside the namespace
- `ip netns exec <ns> ip route` — Verify routing
- `sudo iptables -t nat -L -n -v` — Inspect NAT rules and packet counters
- `nsenter -t <pid> -n tcpdump -i eth0` — Capture traffic inside a container
- `bridge fdb show` — Inspect the MAC address table on the bridge
- `conntrack -L` — View active connection tracking entries

Most networking issues come down to three things: missing routes, missing iptables rules, or IP forwarding being disabled.

## Wrapping Up

Container networking is not a black box. It's a well-defined stack of Linux primitives — namespaces for isolation, veth pairs for connectivity, bridges for switching, and iptables for NAT and filtering. Docker, Podman, and Kubernetes all build on these same foundations. Once you understand the primitives, you can reason about and debug any container networking scenario, whether it's a simple bridge setup or a complex multi-host overlay network.
