package main

import (
	"sync/atomic"

	"github.com/alecthomas/kingpin/v2"
	"github.com/bwmarrin/discordgo"
	"github.com/darenliang/dsfs/fuse"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/pprofhandler"
	"go.uber.org/zap"
)

var (
	token     string
	userToken bool
	guildID   string
	mount     string
	compact   bool
	cache     string
	debug     bool
	options   []string
	// We need to jankily expose dsfs for event handlers
	dsfs      *Dsfs
	dsfsReady = &atomic.Bool{}
)

func main() {
	kingpin.Flag("token", "Token").Short('t').Required().StringVar(&token)
	kingpin.Flag("server", "Guild ID").Short('s').Required().StringVar(&guildID)
	kingpin.Flag("user", "Token is a user token").Short('u').BoolVar(&userToken)
	kingpin.Flag("mount", "Mount point").Short('m').StringVar(&mount)
	kingpin.Flag("compact", "Compact transactions").Short('x').BoolVar(&compact)
	kingpin.Flag("cache", "Cache type").Short('c').Default("disk").EnumVar(&cache, "disk", "memory")
	kingpin.Flag("verbose", "Enable pprof and print debug logs").Short('v').BoolVar(&debug)
	kingpin.Flag("options", "FUSE options").Short('o').StringsVar(&options)
	kingpin.Parse()

	// Setup logger and debug endpoint if specified
	logger, _ := zap.NewDevelopment()
	if debug {
		zap.ReplaceGlobals(logger)
		go func() {
			zap.S().Info("pprof running on port 8000")
			_ = fasthttp.ListenAndServe(":8000", pprofhandler.PprofHandler)
		}()
	} else {
		zap.ReplaceGlobals(logger.WithOptions(zap.IncreaseLevel(zap.InfoLevel)))
	}
	defer logger.Sync()

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

	dsfs = NewDsfs(dg, db, writer, txChannel, dataChannel, cache)
	dsfsReady.Store(true)

	host := fuse.NewFileSystemHost(dsfs)
	host.SetCapReaddirPlus(true)
	host.Mount(mount, FuseArgs(options))
}
