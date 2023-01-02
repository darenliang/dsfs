package main

import (
	"flag"
	"github.com/bwmarrin/discordgo"
	"github.com/darenliang/dsfs/fuse"
	"go.uber.org/atomic"
	"go.uber.org/zap"
	"net/http"
	_ "net/http/pprof"
)

var (
	token         string
	userToken     bool
	guildID       string
	mount         string
	compact       bool
	debug         bool
	options       FuseOpts
	requiredFlags = []string{"t", "s"}
	// We need to jankily expose dsfs for event handlers
	dsfs      *Dsfs
	dsfsReady = atomic.NewBool(false)
)

func main() {
	flag.StringVar(&token, "t", "", "Token")
	flag.BoolVar(&userToken, "u", false, "Token is a user token")
	flag.StringVar(&guildID, "s", "", "Guild ID")
	flag.StringVar(&mount, "m", "", "Mount point")
	flag.BoolVar(&compact, "c", false, "Compact transactions")
	flag.BoolVar(&debug, "d", false, "Enable pprof and print debug logs")
	flag.Var(&options, "o", "FUSE options")
	flag.Parse()

	// Setup logger and debug endpoint if specified
	logger, _ := zap.NewDevelopment()
	if debug {
		zap.ReplaceGlobals(logger)
		go func() {
			zap.S().Info("pprof running on port 8000")
			_ = http.ListenAndServe(":8000", nil)
		}()
	} else {
		zap.ReplaceGlobals(logger.WithOptions(zap.IncreaseLevel(zap.InfoLevel)))
	}
	defer logger.Sync()

	// Handle missing required arguments
	seen := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	for _, requiredFlag := range requiredFlags {
		if !seen[requiredFlag] {
			zap.S().Errorf("missing required -%s argument", requiredFlag)
			return
		}
	}

	var tokenPrefix string
	if !userToken {
		tokenPrefix = "Bot "
	}

	dg, err := discordgo.New(tokenPrefix + token)
	if err != nil {
		zap.S().Error("error creating Discord session,", err)
		return
	}

	dg.AddHandler(messageCreate)
	dg.Identify.Intents = discordgo.IntentsGuildMessages

	err = dg.Open()
	if err != nil {
		zap.S().Error("error opening connection,", err)
		return
	}

	txChannel, dataChannel, err := prepareChannels(dg, guildID)
	if err != nil {
		zap.S().Error(err)
		return
	}

	db, err := setupDB(dg, txChannel, compact)
	if err != nil {
		zap.S().Error(err)
		return
	}

	writer := setupWriter(dg, txChannel.ID, dataChannel.ID)

	dsfs = NewDsfs(dg, db, writer, txChannel, dataChannel)
	dsfsReady.Store(true)

	host := fuse.NewFileSystemHost(dsfs)
	host.SetCapReaddirPlus(true)
	host.Mount(mount, options.Args())
}
