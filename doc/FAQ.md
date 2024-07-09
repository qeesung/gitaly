
<!---
The FAQ page serves as an index for Gitaly-related questions.
While our Gitaly documentation is comprehensive,
users sometimes need to visit multiple sections
to gather information and solve specific problems.
The FAQ page is designed in a question-and-answer format,
similar to a conversation between a student and a mentor,
to help users learn from previous experiences and find solutions quickly.

The FAQ page enhances the Gitaly documentation but does not replace it.
The Gitaly documentation remains the single source of truth.

A Q&A is recommended to follow this structure:

1. A Concise Title
2. Keywords that can be useful for text searching
3. Background and Details of the Question:
   * When did the problem occur?
   * In what form did it manifest?
4. Solution:
   * We don't recommend including too many technical details in a Q&A.
     Sometimes a single command will suffice.
   * Since the FAQ page's primary purpose is to serve as an index,
     the first place users go to, if more technical details are needed,
     consider adding a separate markdown file and linking to it from the Q&A.
5. References: Where to find related information in our
Gitaly documentation or other resources.

-->

### How to Enable PostgreSQL Database When Running Gitaly Unit Tests
Keywords: localhost, unittest, PostgreSQL

If you encounter the following error when running Gitaly unit tests:
```
...
failed to connect to `host=127.0.0.1 user=peijian database=postgres: server error (FATAL: role "peijian" does not exist (SQLSTATE 28000))`
...
```

This indicates that PostgreSQL is not running locally.
You can further test the database connection using the command:
`go test -v -count=1 ./internal/praefect/datastore/glsql -run=^TestOpenDB$`
as mentioned in the [1].

To resolve this, you can run a PostgreSQL database using a Docker image.
Choose one of the following options:
1. Set the environment variable PGUSER to postgres and run[2]:
```shell
docker run --name praefect-pg -p 5432:5432  -e POSTGRES_HOST_AUTH_METHOD=trust  -d postgres:14.9`;
```
2. Alternatively, run the PostgreSQL Docker container with your PostgreSQL username:
```
docker run --name praefect-pg-gitaly-unittest -p 5432:5432 -e POSTGRES_USER=peijian -e POSTGRES_HOST_AUTH_METHOD=trust  -d postgres:14.9`
```

References:
- [1] [internal/praefect/datastore/glsql/doc.go](https://gitlab.com/gitlab-org/gitaly/-/blob/master/internal/praefect/datastore/glsql/doc.go#L18)
- [2] [Beginners Guide](https://gitlab.com/gitlab-org/gitaly/-/blob/master/doc/beginners_guide.md?ref_type=heads#praefect-tests)
- [3] [How to Use the Postgres Docker Official Image](https://www.docker.com/blog/how-to-use-the-postgres-docker-official-image/)

