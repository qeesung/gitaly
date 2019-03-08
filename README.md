# ![Gitaly](https://gitlab.com/gitlab-org/gitaly/uploads/509123ed56bd51247996038c858db006/gitaly-wordmark-small.png)

**Quick Links**:
  [**Roadmap**][roadmap] |
  [Want to Contribute?](https://gitlab.com/gitlab-org/gitaly/issues?scope=all&utf8=%E2%9C%93&state=opened&label_name[]=Accepting%20merge%20requests) |
  [GitLab Gitaly Issues](https://gitlab.com/groups/gitlab-org/-/issues?scope=all&state=opened&utf8=%E2%9C%93&label_name%5B%5D=Gitaly) |
  [GitLab Gitaly Merge Requests](https://gitlab.com/groups/gitlab-org/-/merge_requests?label_name%5B%5D=Gitaly) |
  [GitLab.com Monitoring Dashboard][dashboards]

--------------------------------------------

Gitaly is a Git [RPC](https://en.wikipedia.org/wiki/Remote_procedure_call)
service for handling all the git calls made by GitLab.

To see where it fits in please look at [GitLab's architecture](https://docs.gitlab.com/ce/development/architecture.html#system-layout).

## Project Goals

Fault-tolerant horizontal scaling of Git storage in GitLab, and particularly, on [gitlab.com](https://gitlab.com).

This will be achieved by focusing on two areas (in this order):

  1. **Migrate from repository access via NFS to gitaly-proto, GitLab's new Git RPC protocol**
  1. **Evolve from large Gitaly servers managed as "pets" to smaller Gitaly servers that are "cattle"**

## Current Status

As of GitLab 11.5, almost all application code accesses Git repositories
through Gitaly instead of direct disk access. GitLab.com production no
longer uses direct disk access to touch Git repositories; the [NFS
mounts have been
removed](https://about.gitlab.com/2018/09/12/the-road-to-gitaly-1-0/).

For performance reasons some RPCs can be performed through NFS still. An
effort is made to mitigate performance issues by removing [Gitaly N+1](https://gitlab.com/groups/gitlab-org/-/epics/827).
Once that is no longer neccesairy we can conclude the migration project by 
[removing the Git repository storage paths from gitlab-rails's
configuration](https://gitlab.com/gitlab-org/gitaly/issues/1282).

In the meantime we are building features according to our [roadmap][roadmap].

If you're interested in seeing how well Gitaly is performing on
GitLab.com, we have [dashboards][dashboards]!

##### Overall

[![image](https://gitlab.com/gitlab-org/gitaly/uploads/ca7dddd2e23b7f1fb8c0f842c93059ce/gitaly-overview_s.png)](https://dashboards.gitlab.com/d/000000176/gitaly)

##### By Feature

[![image](https://gitlab.com/gitlab-org/gitaly/uploads/048a1facaaf18b4799569150ca7c3cd6/gitaly-features_s.png)](https://dashboards.gitlab.com/d/000000198/gitaly-features-overview)

## Installation

Most users won't install Gitaly on its own. It is already included in
[your GitLab installation](https://about.gitlab.com/install/).

Gitaly requires Go 1.10 or newer and Ruby 2.5. Run `make` to download
and compile Ruby dependencies, and to compile the Gitaly Go
executable.

Gitaly uses `git`. Version `2.18.0` or higher is required.

## Configuration

See [configuration documentation](doc/configuration).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Name

Gitaly is a tribute to git and the town of [Aly](https://en.wikipedia.org/wiki/Aly). Where the town of
Aly has zero inhabitants most of the year we would like to reduce the number of
disk operations to zero for most actions. It doesn't hurt that it sounds like
Italy, the capital of which is [the destination of all roads](https://en.wikipedia.org/wiki/All_roads_lead_to_Rome). All git actions in
GitLab end up in Gitaly.

## Design

High-level architecture overview:

![Gitaly Architecture](https://docs.google.com/drawings/d/14-5NHGvsOVaAJZl2w7pIli8iDUqed2eIbvXdff5jneo/pub?w=2096&h=1536)

[Edit this diagram directly in Google Drawings](https://docs.google.com/drawings/d/14-5NHGvsOVaAJZl2w7pIli8iDUqed2eIbvXdff5jneo/edit)

### Gitaly clients

As of Q4 2018, the following GitLab components act as Gitaly clients:

-   [gitlab-rails](https://gitlab.com/gitlab-org/gitlab-ce/blob/master/lib/gitlab/gitaly_client.rb):
    the main GitLab Rails application.
-   [gitlab-shell](https://gitlab.com/gitlab-org/gitlab-shell/tree/master):
    for `git clone`, `git push` etc. via SSH.
-   [gitlab-workhorse](https://gitlab.com/gitlab-org/gitlab-workhorse/blob/master/internal/gitaly/gitaly.go):
    for `git clone` via HTTPS and for slow requests that serve raw Git
    data.
    ([example](https://gitlab.com/gitlab-org/gitaly/raw/master/README.md))
-   [gitaly-ssh](https://gitlab.com/gitlab-org/gitaly/tree/master/cmd/gitaly-ssh):
    for internal Git data transfers between Gitaly servers.
-   [gitaly-ruby](https://gitlab.com/gitlab-org/gitaly/blob/master/ruby/lib/gitlab/git/gitaly_remote_repository.rb):
    for RPC's that interact with more than one repository, such as
    merging a branch.

The clients written in Go (gitlab-shell, gitlab-workhorse, gitaly-ssh)
use library code from the
[gitlab.com/gitlab-org/gitaly/client](https://gitlab.com/gitlab-org/gitaly/tree/master/client)
package.

## Further reading

More about the project, and its processes is [accumulated in the docs](doc/index.md).

## Distributed Tracing

Gitaly supports distributed tracing through [LabKit](https://gitlab.com/gitlab-org/labkit/) using [OpenTracing APIs](https://opentracing.io).

By default, no tracing implementation is linked into the binary, but different OpenTracing providers can be linked in using [build tags](https://golang.org/pkg/go/build/#hdr-Build_Constraints)/[build constraints](https://golang.org/pkg/go/build/#hdr-Build_Constraints). This can be done by setting the `BUILD_TAGS` make variable.

For more details of the supported providers, see LabKit, but as an example, for Jaeger tracing support, include the tags: `BUILD_TAGS="tracer_static tracer_static_jaeger"`.

```shell
$ make BUILD_TAGS="tracer_static tracer_static_jaeger"
```

Once Gitaly is compiled with an opentracing provider, the tracing configuration is configured via the `GITLAB_TRACING` environment variable.

For example, to configure Jaeger, you could use the following command:

```shell
GITLAB_TRACING=opentracing://jaeger ./gitaly config.toml
```

## Presentations

-   [How Gitaly fits into GitLab, part 3 (video),
    2019-03-07](https://drive.google.com/file/d/1G3xOpsblfYcCTyzv1Xe6Pg2Ce2TGriTb/view?usp=sharing)
    a 1-hour training video for contributors new to GitLab and Gitaly.
    Contents: a closer look at the final stage of `git push` where the
    git hooks run and the refs get updated. Interaction between the git
    hooks and GitLab internal API. The Git object quarantine mechanism.
    Preview of Git HTTP (to be discussed next time).
-   [How Gitaly fits into GitLab, part 2 (video),
    2019-02-28](https://drive.google.com/file/d/1SFvx49kJkMsvz8YD4chP0y_QJaygSiiH/view?usp=sharing)
    a 1-hour training video for contributors new to GitLab and Gitaly.
    Contents: what is in a gitaly-proto Repository message, legacy vs
    hashed storage (repository directories), `git clone` via SSH,
    gitlab-shell, `authorized_keys` and forced commands, what happens
    during `git push`.
- [How Gitaly fits into GitLab, part 1 (video), 2019-02-21](https://drive.google.com/file/d/1aKlbPVbKr7MueNIyNeIRZIX2u0K4lKQB/view?usp=sharing) a 1-hour training video for contributors new to GitLab and Gitaly. Contents: overview of GitLab backend processes, gitlab-rails deep dive: Gitaly config in gitlab-rails, SQL data model, overview of how Gitaly calls get made via GitalyClient.call.
- [Infrastructure Team Update 2017-05-11](https://about.gitlab.com/2017/05/11/functional-group-updates/#infrastructure-team)
- [Gitaly Basics, 2017-05-01](https://docs.google.com/presentation/d/1cLslUbXVkniOaeJ-r3s5AYF0kQep8VeNfvs0XSGrpA0/edit#slide=id.g1c73db867d_0_0)
- [Git Paris meetup, 2017-02-22](https://docs.google.com/presentation/d/19OZUalFMIDM8WujXrrIyCuVb_oVeaUzpb-UdGThOvAo/edit?usp=sharing) a high-level overview of what our plans are and where we are.

[dashboards]: https://dashboards.gitlab.com/d/000000176/gitaly
[roadmap]: https://gitlab.com/groups/gitlab-org/-/roadmap?label_name%5B%5D=Gitaly&scope=all&sort=start_date_asc&state=opened
