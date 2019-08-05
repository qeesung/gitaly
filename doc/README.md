## Gitaly documentation

The historical reasons for the inception of Gitaly and our design decisions are
written in [the design doc](doc/DESIGN.md).

#### Configuring Gitaly

Running Gitaly requires it to be configured correctly, options are described in
the [configuration documentation](doc/configuration/README.md).

#### Developing Gitaly

- When new to Gitaly development, start by reading the [beginners guide](doc/beginners_guide.md).
- When developing on Gitaly-Ruby, read the [Gitaly-Ruby doc](doc/ruby_endpoint.md)
- The Gitaly release process is descripted in [our process doc](doc/PROCESS.md)
- Tests use Git repositories too, [read more about them](doc/test_repos.md)

#### Gitaly HA

Gitaly does not replicate any data. If a Gitaly server goes down, any of its
clients can't read or write to the repositories stored on that server. This
means that Gitaly is not highly available. How this will be solved is described
[in the HA design document](doc/design_ha.md)

For configuration please read [praefects configuration documentation](doc/configuration/praefect.md).

#### Technical explanations

- [Delta Islands](doc/delta_islands.md)
