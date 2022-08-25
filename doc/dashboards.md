# Observibility

## Logging

Gitaly leverages the [logrus](https://github.com/sirupsen/logrus) for exposing
log messages. Most request messages are created in the [logrus middleware][logrus-middleware].

Messages can be queried leveraging our [Kibana instance](https://log.gitlab.net).

[logrus-middleware]: https://github.com/grpc-ecosystem/go-grpc-middleware/tree/master/logging/logrus

## Prometheus

Gitaly emits low cardinality metrics through Prometheus. Most of these are added
by [go-grpc-prometheus](https://github.com/grpc-ecosystem/go-grpc-prometheus).
Many custom metrics were also added.

### Grafana

To display the prometheus metrics, GitLab leverages Grafana. The instance is
available only for GitLab team members to view the dashboards. The dashboards can be found at:
[dashboards.gitlab.net](https://dashboards.gitlab.net).

#### Editing Gitaly dashboards

Use the Grafana web interface to make changes to dashboards if
necessary.

**Remember to hit the 'Save' button at the top of the Grafana screen when making changes.**

##### Tiled (repeated) dashboards

If you want to make a change across a tiled Grafana dashboard such as
the [feature request rate
overview](https://dashboards.gitlab.net/dashboard/db/gitaly-features-overview),
then edit the first tile (top left). Its settings get applied to the
other tiles as well. If you edit any tile other than the first your
changes will be lost.

##### Drop-down values

At the top of most of our Grafana dashboards you find drop-down menus
for GRPC method names, Prometheus jobs etc. The possible values in these
drop-downs are defined with Prometheus queries. To see or change these
queries go into the dashboard's global settings (the gear icon at the
top of the page) and look in the 'Templating' section. You can then edit
entries.

Note that Grafana 'templates' use a combination of PromQL and
Grafana-specific modifiers.

## Sentry

Errors are tracked in our sentry instance, and due to their sensitive nature only viewable
by developers at GitLab at [the error tracking page](https://gitlab.com/gitlab-org/gitaly/error_tracking).
