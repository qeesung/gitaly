# Test cluster with Praefect and multiple Gitaly servers

This directory contains a
[docker-compose.yml](https://docs.docker.com/compose/) that has Praefect and 3 Gitaly servers
behind it. This setup is meant for testing purposes only and SHOULD NOT be used
in production environments.

Boot the cluster with `docker-compose up`. After some time you can connect to praefect on port 2305
