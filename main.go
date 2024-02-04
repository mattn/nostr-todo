package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/google/uuid"
	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/urfave/cli/v3"
	"golang.org/x/exp/slices"
)

const name = "nostr-todo"

const version = "0.0.4"

var revision = "HEAD"

var ErrNotFound = errors.New("todo list not found")

type Config struct {
	Relays     []string `json:"relays"`
	PrivateKey string   `json:"privatekey"`
}

func configDir() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		dir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, ".config"), nil
	default:
		return os.UserConfigDir()
	}
}

func loadConfig(profile string) (*Config, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "nostr-todo")

	var fp string
	if profile == "" {
		fp = filepath.Join(dir, "config.json")
	} else if profile == "?" {
		names, err := filepath.Glob(filepath.Join(dir, "config-*.json"))
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			name = filepath.Base(name)
			name = strings.TrimLeft(name[6:len(name)-5], "-")
			fmt.Println(name)
		}
		os.Exit(0)
	} else {
		fp = filepath.Join(dir, "config-"+profile+".json")
	}
	os.MkdirAll(filepath.Dir(fp), 0700)

	b, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	var cfg Config
	err = json.Unmarshal(b, &cfg)
	if err != nil {
		return nil, err
	}
	if len(cfg.Relays) == 0 {
		cfg.Relays = []string{"wss://yabu.me"}
	}
	return &cfg, nil
}

type Todo struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	Done      bool   `json:"done"`
	CreatedAt int64  `json:"created_at"`
}

type TodoList []Todo

func tagName(name string) string {
	if name == "" {
		return "nostr-todo"
	}
	return "nostr-todo-" + name

}

func (tl *TodoList) MarshalJSON() ([]byte, error) {
	return json.Marshal(*tl)
}

func (tl *TodoList) UnmarshalJSON(b []byte) error {
	return json.Unmarshal(b, (*[]Todo)(tl))
}

func (tl *TodoList) Sort() {
	sort.Slice(*tl, func(i, j int) bool {
		return (*tl)[i].CreatedAt < (*tl)[j].CreatedAt
	})
}

func (tl *TodoList) Load(ctx context.Context, cfg *Config, name string) error {
	pool := nostr.NewSimplePool(ctx)
	filter := nostr.Filter{
		Kinds: []int{nostr.KindApplicationSpecificData},
		Tags: nostr.TagMap{
			"d": []string{tagName(name)},
		},
	}
	ev := pool.QuerySingle(ctx, cfg.Relays, filter)
	if ev == nil {
		return ErrNotFound
	}

	return tl.UnmarshalJSON([]byte(ev.Content))
}

func (tl *TodoList) Save(ctx context.Context, cfg *Config, name string) error {
	b, err := tl.MarshalJSON()
	if err != nil {
		return err
	}
	var sk string
	var pub string
	if _, s, err := nip19.Decode(cfg.PrivateKey); err == nil {
		sk = s.(string)
		if _, err = nostr.GetPublicKey(s.(string)); err != nil {
			return err
		}
	} else {
		return err
	}

	newev := nostr.Event{
		Kind:      nostr.KindApplicationSpecificData,
		Content:   string(b),
		CreatedAt: nostr.Now(),
		PubKey:    pub,
		Tags: nostr.Tags{
			{"d", tagName(name)},
		},
	}

	if err := newev.Sign(sk); err != nil {
		return err
	}

	var wg sync.WaitGroup
	for _, relayURL := range cfg.Relays {
		wg.Add(1)

		relayURL := relayURL
		go func() {
			defer wg.Done()

			relay, err := nostr.RelayConnect(ctx, relayURL)
			if err != nil {
				log.Println(err)
				return
			}
			defer relay.Close()
			relay.Publish(ctx, newev)
		}()

	}
	wg.Wait()
	return nil
}

func doNew(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Present() {
		return cli.ShowSubcommandHelp(cmd)
	}
	cfg := cmd.Root().Metadata["config"].(*Config)
	name := cmd.String("name")

	var todolist TodoList
	err := todolist.Load(ctx, cfg, name)
	if err != nil && err != ErrNotFound {
		return err
	}
	todolist = append(todolist, Todo{
		ID:        uuid.New().String(),
		Content:   cmd.String("content"),
		Done:      false,
		CreatedAt: time.Now().Unix(),
	})
	todolist.Sort()

	return todolist.Save(ctx, cfg, name)
}

func doList(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Present() {
		return cli.ShowSubcommandHelp(cmd)
	}
	cfg := cmd.Root().Metadata["config"].(*Config)
	name := cmd.String("name")
	showAll := cmd.Bool("a")

	var todolist TodoList
	err := todolist.Load(ctx, cfg, name)
	if err != nil {
		return err
	}
	for _, todo := range todolist {
		if todo.Done && !showAll {
			continue
		}

		mark := "　"
		if todo.Done {
			mark = "✅"
		}
		fmt.Printf("%s (%s): %s %s\n",
			color.GreenString(todo.ID),
			color.BlueString(time.Unix(todo.CreatedAt, 0).Format("2006-01-02T15-04-05")),
			mark,
			todo.Content)
	}
	return nil
}

func doDone(ctx context.Context, cmd *cli.Command) error {
	if !cmd.Args().Present() {
		return cli.ShowSubcommandHelp(cmd)
	}
	cfg := cmd.Root().Metadata["config"].(*Config)
	name := cmd.String("name")

	var todolist TodoList
	err := todolist.Load(ctx, cfg, name)
	if err != nil {
		return err
	}
	for _, arg := range cmd.Args().Slice() {
		for i := 0; i < len(todolist); i++ {
			if todolist[i].ID != arg {
				continue
			}
			todolist[i].Done = true
		}
	}

	return todolist.Save(ctx, cfg, name)
}

func doUndone(ctx context.Context, cmd *cli.Command) error {
	if !cmd.Args().Present() {
		return cli.ShowSubcommandHelp(cmd)
	}
	cfg := cmd.Root().Metadata["config"].(*Config)
	name := cmd.String("name")

	var todolist TodoList
	err := todolist.Load(ctx, cfg, name)
	if err != nil {
		return err
	}
	for _, arg := range cmd.Args().Slice() {
		for i := 0; i < len(todolist); i++ {
			if todolist[i].ID != arg {
				continue
			}
			todolist[i].Done = false
		}
	}

	return todolist.Save(ctx, cfg, name)
}

func doEdit(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Len() != 1 {
		return cli.ShowSubcommandHelp(cmd)
	}
	cfg := cmd.Root().Metadata["config"].(*Config)
	name := cmd.String("name")

	var todolist TodoList
	err := todolist.Load(ctx, cfg, name)
	if err != nil {
		return err
	}
	for i := 0; i < len(todolist); i++ {
		if todolist[i].ID != cmd.Args().First() {
			continue
		}
		todolist[i].Content = cmd.String("content")
	}

	return todolist.Save(ctx, cfg, name)
}

func doDelete(ctx context.Context, cmd *cli.Command) error {
	if !cmd.Args().Present() {
		return cli.ShowSubcommandHelp(cmd)
	}
	cfg := cmd.Root().Metadata["config"].(*Config)
	name := cmd.String("name")

	var todolist TodoList
	err := todolist.Load(ctx, cfg, name)
	if err != nil {
		return err
	}

	for _, arg := range cmd.Args().Slice() {
		todolist = slices.DeleteFunc(todolist, func(todo Todo) bool {
			return todo.ID == "" || todo.ID == arg
		})
	}

	return todolist.Save(ctx, cfg, name)
}

func doVersion(ctx context.Context, cmd *cli.Command) error {
	if cmd.Args().Present() {
		return cli.ShowSubcommandHelp(cmd)
	}
	fmt.Println(version)
	return nil
}

var commands = []*cli.Command{
	{
		Name:      "list",
		Usage:     "list todos",
		UsageText: "nostr-todo list",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   "",
			},
			&cli.BoolFlag{
				Name:  "a",
				Usage: "list all todos",
			},
		},
		Action: doList,
	},
	{
		Name:      "new",
		Usage:     "new todo",
		UsageText: "nostr-todo new",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   "",
			},
			&cli.StringFlag{
				Name:     "content",
				Usage:    "content",
				Required: true,
			},
		},
		Action: doNew,
	},
	{
		Name:      "done",
		Usage:     "done todo",
		UsageText: "nostr-todo done [id...]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   "",
			},
		},
		Action: doDone,
	},
	{
		Name:      "undone",
		Usage:     "undone todo",
		UsageText: "nostr-todo undone [id...]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   "",
			},
		},
		Action: doUndone,
	},
	{
		Name:      "edit",
		Usage:     "edit todo",
		UsageText: "nostr-todo edit [id]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   "",
			},
		},
		Action: doEdit,
	},
	{
		Name:      "delete",
		Usage:     "delete todo",
		UsageText: "nostr-todo delete [id...]",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "name",
				Aliases: []string{"n"},
				Value:   "",
			},
		},
		Action: doDelete,
	},
	{
		Name:      "version",
		Usage:     "show version",
		UsageText: "nostr-todo version",
		Action:    doVersion,
	},
}

func main() {
	cmd := &cli.Command{
		Usage:       "A cli application for nostr",
		Description: "A cli application for nostr",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "a", Usage: "profile name"},
		},
		Commands: commands,
		Metadata: map[string]any{
			"config": Config{},
		},
		Before: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Args().Get(0) == "version" {
				return nil
			}
			profile := cmd.String("a")
			cfg, err := loadConfig(profile)
			if err != nil {
				return err
			}
			cmd.Root().Metadata["config"] = cfg
			return nil
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
