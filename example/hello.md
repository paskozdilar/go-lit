# Hello, literate Go

The program greets whoever is named on the command line.

```go
// file: main.go
package main

func main() {
	//:: parse_args :://
	//:: greet :://
}
```

We default to "world" when no name is given.

```go
//== parse_args ==//
name := "world"
if len(os.Args) > 1 {
	name = strings.Join(os.Args[1:], " ")
}
```

Greeting is a one-liner, but it uses a helper defined at top level.

```go
//== greet ==//
fmt.Println(greeting(name))
```

Top-level declarations can simply be appended to the same file, in any order:

```go
// file: main.go
func greeting(name string) string {
	return "Hello, " + name + "!"
}
```

This block has no marker, so it is just illustration and is ignored:

```go
greeting("ignored")
```
