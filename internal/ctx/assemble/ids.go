package assemble

// The fragments (internal/prompts/texts/<id>.md) this package gives agents: every one of them,
// and each used (ids_test.go).
const (
	idIridiumOpen      = "iridium.open" // the system prompt's own block
	idIridiumClose     = "iridium.close"
	idHostIntro        = "host.intro"         // a session hosted here: what its ir reaches
	idCLI              = "cli"                // heads the CLI section
	idCLIHost          = "cli.host"           // ir api and ir run at the host: {{secrets}}
	idCLIRemote        = "cli.remote"         // ir api at another machine, and what it does not reach
	idCLIDiscover      = "cli.discover"       // how to read ir api help: {{topics}}
	idHostSecretsFiles = "host.secrets-files" // how to ask for a secret, where wake is told
	idLinks            = "links"              // heads the Links section
	idLinksFiles       = "links.files"        // how to hand over a file
	idRuntime          = "runtime"            // heads the Runtime section
	idHostManual       = "host.manual"        // Claude: how to ask for manual approval, auto mode having denied a step
	idShellIR          = "shell.ir"           // a command line at a computer reaches the Iridium there, with its host's rights
	idNames            = "names"              // how words name a session or a folder
	idTitle            = "title"              // the Title section: title the session
	idTitleNow         = "title.now"          // after a message, now and then: the session's title as it is
	idTitleNone        = "title.none"         // the same for a session with none yet
	idTitleNowOf       = "title.now-channel"  // the same, of a channel the agent is in beside its own
	idTitleNoneOf      = "title.none-channel" // and of one with none yet
	idLang             = "lang"               // the user's language: {{language}}
	idLangZh           = "lang.zh"            // a language's name, for lang
	idLangEn           = "lang.en"
	idRemoteIntro      = "remote.intro"       // a session at another machine that has ir there
	idRemoteIntroBare  = "remote.intro-bare"  // one without ir
	idMessageTag       = "message.tag"        // before a message: who it is from, {{tag}}
	idMessageAttach    = "message.attachment" // a file that came with a message: {{path}}
	idAskDenied        = "ask.denied"         // a no without words
	idWhereHost        = "where.host"         // where it runs: the host, {{os}}, {{folder}}
	idWhereRemote      = "where.remote"       // at {{computer}}: how paths are written from there

	// A channel of several agents (channel.go).
	idChannelMessage     = "channel.message" // before the words: the message's number
	idChannelQuote       = "channel.quote"   // then the message it answers: its number, who said it
	idChannelQuoteEnd    = "channel.quote-end"
	idChannelGuest       = "channel.context.guest"        // who it is in a channel it is in by a subscription, and how it speaks there
	idChannelGuestBare   = "channel.context.guest-bare"   // the same for an agent with no ir
	idChannelAgain       = "channel.context.again"        // in place of those, given before: which channel this is
	idChannelAgainID     = "channel.context.again-id"     // the same when the channel has no custom name (avoid repeating its id)
	idChannelRename      = "channel.context.rename"       // its nick is a default one: name itself first
	idChannelSendUnseen  = "channel.send.unseen"          // heads what an agent's ir is answered with: said since it last read
	idChannelSendBehind  = "channel.send.behind"          // its ir refused: too far behind
	idChannelSendNamed   = "channel.send.named"           // its ir refused: something unseen names it
	idChannelSilent      = "channel.silent"               // to the agent that started one that said nothing where it was called
	idChannelOpen        = "channel.context.open"         // the block of what the channel said
	idChannelMembers     = "channel.context.members"      // who the agent is there, who else, how to call
	idChannelMembersBare = "channel.context.members-bare" // the same without ir: no id to write
	idChannelSkipped     = "channel.context.skipped"      // the earliest messages, not shown
	idChannelListed      = "channel.context.listed"       // heads the turns listed
	idChannelTurn        = "channel.context.turn"         // a turn in one line
	idChannelAnswered    = "channel.context.answered"     // who answered in it
	idChannelFull        = "channel.context.full"         // heads the messages in full
	idChannelLine        = "channel.context.line"         // before a message in full: its number, who, when
	idChannelLineDid     = "channel.context.line-did"     // then what its agent did before saying it
	idChannelLineEnd     = "channel.context.line-end"
	idChannelWork        = "channel.context.work"       // heads what each agent did since
	idChannelWorkLine    = "channel.context.work-line"  // an agent's line of it
	idChannelWorkFile    = "channel.context.work-files" // the files it changed

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
	idSelfSection      = "self.section"       // heads the Collaboration & Memory section
	idSelfWho          = "self.who"           // the nick it goes by
	idSelfSession      = "self.session"       // its session's id: what ir takes for it
	idSelfPersona      = "self.persona"       // before its persona's words
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
	idChannelScripts        = "channel.context.scripts"           // the scripts IR runs there
	idChannelPlace          = "channel.context.place"             // the channel's folder, when an agent works elsewhere
	idChannelAway           = "channel.context.away"              // the agent called is one that does
	idChannelRecv           = "channel.context.recv"              // receive sets: any member may change one
	idChannelRules          = "channel.context.rules"             // heads the rules
	idChannelRuleOwn        = "channel.context.rule-own"          // its own channel: only what it says reaches it
	idChannelRuleGuest      = "channel.context.rule-guest"        // another's: what it writes is said there, not with ir
	idChannelRuleGuestBare  = "channel.context.rule-guest-bare"   // the same without ir
	idChannelRuleWork       = "channel.context.rule-work"         // a line before working
	idChannelRuleMention    = "channel.context.rule-mention"      // @ calls, a name names
	idChannelRuleTight      = "channel.context.rule-tight"        // say little; nothing to say: interrupt
	idChannelRuleTightBare  = "channel.context.rule-tight-bare"   // the same without ir
	idChannelProfiles       = "channel.context.profiles"          // heads the cards changed
	idChannelProfileName    = "channel.context.profile-name"      // one renamed
	idChannelProfilePersona = "channel.context.profile-persona"   // before its own new persona (only its own is told)
	idChannelProfileNoPers  = "channel.context.profile-nopersona" // its own persona taken away
	idChannelProfileImage   = "channel.context.profile-image"     // a new face

	// Reactions and messages taken back (channel.go; docs/channels.md "表情" "撤回").
	idChannelReaction    = "channel.reaction"         // a reaction it receives: the message's number, who, the emoji; then the message's words
	idChannelReactionEnd = "channel.reaction-end"     // after those words
	idChannelRecalled    = "channel.context.recalled" // the messages it was given that were taken back since
	idChannelReact       = "channel.context.react"    // how to react to a message, what a reaction it gets looks like

	// Live channel messages overheard by a running agent (channel.go).
	idChannelOverhear      = "channel.overhear"
	idChannelOverhearBare  = "channel.overhear.bare"
	idChannelOverhearClose = "channel.overhear.close"
)
