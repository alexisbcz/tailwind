# tailwind

Tailwind CSS v4 in Go. Build stylesheets without Node.js.

## Install

```sh
go get github.com/alexisbcz/tailwind
```

## Use

```go
package main

import (
	"log"
	"os"

	"github.com/alexisbcz/tailwind"
)

func main() {
	res, err := tailwind.Build(tailwind.Options{Input: "styles/app.css", Minify: true})
	if err != nil {
		log.Fatal(err)
	}
	os.WriteFile("static/app.css", []byte(res.CSS), 0o644)
}
```

Or use the command line tool:

```sh
go run github.com/alexisbcz/tailwind/cmd/tailwind -i styles/app.css -o static/app.css --minify
```

## License

MIT. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
