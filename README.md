# Sweevo

[Sweevo](https://discworld.fandom.com/wiki/Gods#Sweevo) is the god of Cut Timber, or a Gitlab CI utility to execute jobs in a local Docker container.

## Usage

Locally: `sweevo /path/to/repo/.gitlab-ci.yaml a_job_name /path/to/sweevo/config.yaml`

## Configuration

Some settings can be defined in the configuration file.

```yaml
docker:
  mirrors:
    - 'some.mirror.base.url'
```

## Build

The project is built using [Go](https://golang.org/) 1.20+.

Then to execute the incremental build:

    go build

Run the tests:

    go test
