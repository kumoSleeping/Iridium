package assemble

// The fragments (internal/prompts/texts/<id>.md) this package gives agents: every one of them,
// and each used (ids_test.go).
const (
	idHostIntro        = "host.intro"         // a session hosted here: what its ir reaches
	idHostSecretsFiles = "host.secrets-files" // how to ask for a secret, how to hand over a file
	idNames            = "names"              // how words name a session or a folder
	idTitle            = "title"              // title the session
	idLang             = "lang"               // the user's language: {{language}}
	idLangZh           = "lang.zh"            // a language's name, for lang
	idLangEn           = "lang.en"
	idRemoteIntro      = "remote.intro"       // a session at another machine that has ir there
	idRemoteIntroBare  = "remote.intro-bare"  // one without ir
	idMessageTag       = "message.tag"        // before a message: who it is from, {{tag}}
	idMessageAttach    = "message.attachment" // a file that came with a message: {{path}}
	idRecoverCrash     = "recover.crash"      // Iridium's own line after a crash mid-turn
	idRecoverQuota     = "recover.quota"      // and when the quota is back with nothing waiting
	idAskDenied        = "ask.denied"         // a no without words
	idWhereHost        = "where.host"         // where it runs: the host, {{os}}, {{folder}}
	idWhereRemote      = "where.remote"       // at {{computer}}: how paths are written from there

	// A channel of several agents (channel.go).
	idChannelSystem     = "channel.system"  // in place of title: one agent of several
	idChannelMessage    = "channel.message" // before the words: the message's number
	idChannelQuote      = "channel.quote"   // then the message it answers: its number, who said it
	idChannelQuoteEnd   = "channel.quote-end"
	idChannelGuest      = "channel.context.guest"      // who it is in a channel it is in by a subscription, and how it speaks there
	idChannelGuestBare  = "channel.context.guest-bare" // the same for an agent with no ir
	idChannelAgain      = "channel.context.again"      // in place of those, given before: which channel this is
	idChannelRename     = "channel.context.rename"     // its nick is a default one: name itself first
	idChannelSendUnseen = "channel.send.unseen"        // heads what an agent's ir is answered with: said since it last read
	idChannelSendBehind = "channel.send.behind"        // its ir refused: too far behind
	idChannelSendNamed  = "channel.send.named"         // its ir refused: something unseen names it
	idChannelSilent     = "channel.silent"             // to the agent that started one that said nothing where it was called
	idChannelOpen       = "channel.context.open"       // the block of what the channel said
	idChannelMembers    = "channel.context.members"    // who the agent is there, who else, how to call
	idChannelSkipped    = "channel.context.skipped"    // the earliest messages, not shown
	idChannelListed     = "channel.context.listed"     // heads the turns listed
	idChannelTurn       = "channel.context.turn"       // a turn in one line
	idChannelAnswered   = "channel.context.answered"   // who answered in it
	idChannelFull       = "channel.context.full"       // heads the messages in full
	idChannelLine       = "channel.context.line"       // before a message in full: its number, who, when
	idChannelLineDid    = "channel.context.line-did"   // then what its agent did before saying it
	idChannelLineEnd    = "channel.context.line-end"
	idChannelWork       = "channel.context.work"       // heads what each agent did since
	idChannelWorkLine   = "channel.context.work-line"  // an agent's line of it
	idChannelWorkFile   = "channel.context.work-files" // the files it changed

	// What an agent did, in few words (activity.go).
	idDidBash      = "activity.bash"
	idDidCmds      = "activity.cmds"
	idDidFailed    = "activity.failed"
	idDidFiles     = "activity.files"
	idDidRead      = "activity.read"
	idDidWeb       = "activity.web"
	idDidAgents    = "activity.agents"
	idDidOther     = "activity.other"
	idDidSep       = "activity.sep"
	idChannelCut   = "channel.context.cut"  // after one cut short: how to get the rest
	idChannelPull  = "channel.context.pull" // how to read more of the channel
	idChannelClose = "channel.context.close"
	idNL           = "nl" // a line's end

	// What an agent is told of itself (self.go).
	idSelfWho          = "self.who"           // the nick it goes by
	idSelfIntro        = "self.intro"         // before its intro
	idSelfMemory       = "self.memory"        // heads what it chose to remember
	idSelfMemoryLine   = "self.memory-line"   // before one memory: when, where from
	idSelfMemoryMore   = "self.memory-more"   // how many more there are
	idSelfChannels     = "self.channels"      // heads the channels it is in
	idSelfChannelLine  = "self.channel-line"  // before the head of what it last said in one
	idSelfChannelQuiet = "self.channel-quiet" // one it has not spoken in
	idSelfBefore       = "self.before"        // heads its earlier contexts
	idSelfBeforeLine   = "self.before-line"   // before the head of one's first message
	idSelfTools        = "self.tools"         // how to look the records up
	idSelfRemember     = "self.remember"      // how, and when, to remember
	idSelfProfile      = "self.profile"       // profiles can be changed
	idSelfHelpers      = "self.helpers"       // its own subagents first, a session through ir when it must be one

	// The cards of a channel (channel.go).
	idChannelPeople         = "channel.context.people"          // the people that have an intro
	idChannelScripts        = "channel.context.scripts"         // the scripts IR runs there
	idChannelPlace          = "channel.context.place"           // the channel's folder, when an agent works elsewhere
	idChannelAway           = "channel.context.away"            // the agent called is one that does
	idChannelRecv           = "channel.context.recv"            // receive sets, and how to change one
	idChannelProfiles       = "channel.context.profiles"        // heads the cards changed
	idChannelProfileName    = "channel.context.profile-name"    // one renamed
	idChannelProfileIntro   = "channel.context.profile-intro"   // before a new intro
	idChannelProfileNoIntro = "channel.context.profile-nointro" // an intro taken away
	idChannelProfileImage   = "channel.context.profile-image"   // a new face or background

	// Reactions and messages taken back (channel.go; docs/channels.md "表情" "撤回").
	idChannelReaction    = "channel.reaction"         // a reaction it receives: the message's number, who, the emoji; then the message's words
	idChannelReactionEnd = "channel.reaction-end"     // after those words
	idChannelRecalled    = "channel.context.recalled" // the messages it was given that were taken back since
	idChannelReact       = "channel.context.react"    // how to react to a message, what a reaction it gets looks like
)
