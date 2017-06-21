# Release Process

local-volume-provisioner is released on an as-needed basis. The process is as follows:

1. An issue is proposing a new release with a changelog since the last release
2. An OWNER runs `make test` to make sure tests pass
3. An OWNER runs `git tag -a $VERSION` and inserts the changelog and pushes the tag with `git push $VERSION`
4. An OWNER runs `make push` to build and push the image
5. The release issue is closed

