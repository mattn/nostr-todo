# nostr-todo

ToDo list cli app backend by Nostr relay

## Usage

```
NAME:
   nostr-todo - A cli application for nostr

USAGE:
   nostr-todo [global options] [command [command options]] [arguments...]

DESCRIPTION:
   A cli application for nostr

COMMANDS:
   list     list todos
   new      new todo
   done     done todo
   undone   undone todo
   edit     edit todo
   delete   delete todo
   version  show version
   help, h  Shows a list of commands or help for one command

GLOBAL OPTIONS:
   -a value    profile name
   --help, -h  show help (default: false)
```

## Installation

```
go install github.com/mattn/nostr-todo@latest
```

Create your config.json in ~/.config/nostr-todo/config.json like below

```
{
  "relays": ["wss://yabu.me"],
  "privatekey": "nsec1xxxxxx"
}
```

## License

MIT

## Author

Yasuhiro Matsumoto (a.k.a. mattn)
