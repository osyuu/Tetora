package main

// wire_tools.go constructs tool dependency structs from root globals
// and registers tools via internal/tools.

import (
	"context"
	"encoding/json"
	"fmt"

	"tetora/internal/tool"
	"tetora/internal/tools"
)

// buildMemoryDeps constructs MemoryDeps from root memory functions.
func buildMemoryDeps() tools.MemoryDeps {
	return tools.MemoryDeps{
		GetMemory: getMemory,
		SetMemory: func(cfg *Config, role, key, value string) error {
			return setMemory(cfg, role, key, value) // drop variadic priority
		},
		DeleteMemory: deleteMemory,
		SearchMemory: func(cfg *Config, role, query string) ([]tools.MemoryEntry, error) {
			entries, err := searchMemoryFS(cfg, role, query)
			if err != nil {
				return nil, err
			}
			result := make([]tools.MemoryEntry, len(entries))
			for i, e := range entries {
				result[i] = tools.MemoryEntry{Key: e.Key, Value: e.Value}
			}
			return result, nil
		},
	}
}

// buildImageGenDeps constructs ImageGenDeps from the global limiter.
func buildImageGenDeps() tools.ImageGenDeps {
	return tools.ImageGenDeps{
		GetLimiter: func(ctx context.Context) *tools.ImageGenLimiter {
			app := appFromCtx(ctx)
			if app == nil {
				return nil
			}
			return app.ImageGenLimiter
		},
	}
}

// buildTaskboardDeps constructs TaskboardDeps by wrapping root handler factories.
func buildTaskboardDeps(cfg *Config) tools.TaskboardDeps {
	return tools.TaskboardDeps{
		ListHandler:      toolTaskboardList(cfg),
		GetHandler:       toolTaskboardGet(cfg),
		CreateHandler:    toolTaskboardCreate(cfg),
		MoveHandler:      toolTaskboardMove(cfg),
		CommentHandler:   toolTaskboardComment(cfg),
		DecomposeHandler: toolTaskboardDecompose(cfg),
	}
}

// buildDailyDeps constructs DailyDeps from root handler functions.
func buildDailyDeps(cfg *Config) tools.DailyDeps {
	return tools.DailyDeps{
		WeatherCurrent: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.WeatherCurrent(ctx, cfg.Weather.Location, input)
		},
		WeatherForecast: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.WeatherForecast(ctx, cfg.Weather.Location, input)
		},
		CurrencyConvert: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.CurrencyConvert(ctx, input)
		},
		CurrencyRates: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.CurrencyRates(ctx, input)
		},
		RSSRead: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.RSSRead(ctx, cfg.RSS.Feeds, input)
		},
		RSSList: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.RSSList(ctx, cfg.RSS.Feeds, input)
		},
		Translate: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.Translate(ctx, cfg.Translate.Provider, cfg.Translate.APIKey, input)
		},
		DetectLanguage: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.DetectLanguage(ctx, cfg.Translate.Provider, cfg.Translate.APIKey, input)
		},
		NoteCreate:     toolNoteCreate,
		NoteRead:       toolNoteRead,
		NoteAppend:     toolNoteAppend,
		NoteList:       toolNoteList,
		NoteSearch:     toolNoteSearch,
		StoreLesson:    toolStoreLesson,
		NoteDedup:      toolNoteDedup,
		SourceAudit:    toolSourceAudit,
		WebCrawl:       toolWebCrawl,
		SourceAuditURL: toolSourceAuditURL,
		AudioNormalize: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.AudioNormalize(ctx, input)
		},
	}
}

// buildCoreDeps constructs CoreDeps from root handler functions.
func buildCoreDeps() tools.CoreDeps {
	return tools.CoreDeps{
		ExecHandler:    toolExec,
		ReadHandler:    toolRead,
		WriteHandler:   toolWrite,
		EditHandler:    toolEdit,
		WebSearchHandler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.WebSearch(ctx, tool.WebSearchConfig{
				Provider:   cfg.Tools.WebSearch.Provider,
				APIKey:     cfg.Tools.WebSearch.APIKey,
				BaseURL:    cfg.Tools.WebSearch.BaseURL,
				MaxResults: cfg.Tools.WebSearch.MaxResults,
			}, input)
		},
		WebFetchHandler:      toolWebFetch,
		SessionListHandler:   toolSessionList,
		MessageHandler:       toolMessage,
		CronListHandler:      toolCronList,
		CronCreateHandler:    toolCronCreate,
		CronDeleteHandler:    toolCronDelete,
		AgentListHandler:     toolAgentList,
		AgentDispatchHandler: toolAgentDispatch,
		AgentMessageHandler:  toolAgentMessage,
		SearchToolsHandler:   toolSearchTools,
		ExecuteToolHandler:   toolExecuteTool,
		ImageAnalyzeHandler: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			return tool.ImageAnalyze(ctx, tool.VisionConfig{
				Provider:     cfg.Tools.Vision.Provider,
				APIKey:       cfg.Tools.Vision.APIKey,
				Model:        cfg.Tools.Vision.Model,
				MaxImageSize: cfg.Tools.Vision.MaxImageSize,
				BaseURL:      cfg.Tools.Vision.BaseURL,
			}, input)
		},
	}
}

// buildLifeDeps constructs LifeDeps from root handler functions.
func buildLifeDeps() tools.LifeDeps {
	return tools.LifeDeps{
		TaskCreate:       toolTaskCreate,
		TaskList:         toolTaskList,
		TaskComplete:     toolTaskComplete,
		TaskReview:       toolTaskReview,
		TodoistSync:      toolTodoistSync,
		NotionSync:       toolNotionSync,
		ExpenseAdd:       toolExpenseAdd,
		ExpenseReport:    toolExpenseReport,
		ExpenseBudget:    toolExpenseBudget,
		PriceWatch:       toolPriceWatch,
		ContactAdd:       toolContactAdd,
		ContactSearch:    toolContactSearch,
		ContactList:      toolContactList,
		ContactUpcoming:  toolContactUpcoming,
		ContactLog:       toolContactLog,
		LifeReport:       toolLifeReport,
		LifeInsights:     toolLifeInsights,
		ScheduleView:     toolScheduleView,
		ScheduleSuggest:  toolScheduleSuggest,
		SchedulePlan:     toolSchedulePlan,
		HabitCreate:      toolHabitCreate,
		HabitLog:         toolHabitLog,
		HabitStatus:      toolHabitStatus,
		HabitReport:      toolHabitReport,
		HealthLog:        toolHealthLog,
		HealthSummary:    toolHealthSummary,
		GoalCreate:       toolGoalCreate,
		GoalList:         toolGoalList,
		GoalUpdate:       toolGoalUpdate,
		GoalReview:       toolGoalReview,
		BriefingMorning:  toolBriefingMorning,
		BriefingEvening:  toolBriefingEvening,
		TimeStart:        toolTimeStart,
		TimeStop:         toolTimeStop,
		TimeLog:          toolTimeLog,
		TimeReport:       toolTimeReport,
		QuickCapture:     toolQuickCapture,
		LifecycleSync:    toolLifecycleSync,
		LifecycleSuggest: toolLifecycleSuggest,
		UserProfileGet:   toolUserProfileGet,
		UserProfileSet:   toolUserProfileSet,
		MoodCheck:        toolMoodCheck,
		FamilyListAdd:    toolFamilyListAdd,
		FamilyListView:   toolFamilyListView,
		UserSwitch:       toolUserSwitch,
		FamilyManage:     toolFamilyManage,
	}
}

// buildIntegrationDeps constructs IntegrationDeps from root handler functions.
func buildIntegrationDeps(cfg *Config) tools.IntegrationDeps {
	return tools.IntegrationDeps{
		EmailList:   toolEmailList,
		EmailRead:   toolEmailRead,
		EmailSend:   toolEmailSend,
		EmailDraft:  toolEmailDraft,
		EmailSearch: toolEmailSearch,
		EmailLabel:  toolEmailLabel,

		CalendarList:   toolCalendarList,
		CalendarCreate: toolCalendarCreate,
		CalendarUpdate: toolCalendarUpdate,
		CalendarDelete: toolCalendarDelete,
		CalendarSearch: toolCalendarSearch,

		TweetPost:         toolTweetPost,
		TweetReadTimeline: toolTweetTimeline,
		TweetSearch:       toolTweetSearch,
		TweetReply:        toolTweetReply,
		TweetDM:           toolTweetDM,

		BrowserNavigate:   toolBrowserRelay("navigate"),
		BrowserContent:    toolBrowserRelay("content"),
		BrowserClick:      toolBrowserRelay("click"),
		BrowserType:       toolBrowserRelay("type"),
		BrowserScreenshot: toolBrowserRelay("screenshot"),
		BrowserEval:       toolBrowserRelay("eval"),

		NotebookLMImport:       toolNotebookLMImport,
		NotebookLMListSources:  toolNotebookLMListSources,
		NotebookLMQuery:        toolNotebookLMQuery,
		NotebookLMDeleteSource: toolNotebookLMDeleteSource,

		HAListEntities: toolHAListEntities,
		HAGetState:     toolHAGetState,
		HACallService:  toolHACallService,
		HASetState:     toolHASetState,

		IMessageSend: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			var args struct {
				ChatGUID string `json:"chat_guid"`
				Text     string `json:"text"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}
			if args.ChatGUID == "" || args.Text == "" {
				return "", fmt.Errorf("chat_guid and text are required")
			}
			app := appFromCtx(ctx)
			if app == nil || app.IMessage == nil {
				return "", fmt.Errorf("iMessage bot not initialized")
			}
			if err := app.IMessage.SendMessage(args.ChatGUID, args.Text); err != nil {
				return "", err
			}
			return fmt.Sprintf("message sent to %s", args.ChatGUID), nil
		},
		IMessageSearch: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}
			if args.Query == "" {
				return "", fmt.Errorf("query is required")
			}
			if args.Limit <= 0 {
				args.Limit = 10
			}
			app := appFromCtx(ctx)
			if app == nil || app.IMessage == nil {
				return "", fmt.Errorf("iMessage bot not initialized")
			}
			messages, err := app.IMessage.SearchMessages(args.Query, args.Limit)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(messages)
			return string(b), nil
		},
		IMessageRead: func(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
			var args struct {
				ChatGUID string `json:"chat_guid"`
				Limit    int    `json:"limit"`
			}
			if err := json.Unmarshal(input, &args); err != nil {
				return "", fmt.Errorf("invalid input: %w", err)
			}
			if args.ChatGUID == "" {
				return "", fmt.Errorf("chat_guid is required")
			}
			if args.Limit <= 0 {
				args.Limit = 20
			}
			app := appFromCtx(ctx)
			if app == nil || app.IMessage == nil {
				return "", fmt.Errorf("iMessage bot not initialized")
			}
			messages, err := app.IMessage.ReadRecentMessages(args.ChatGUID, args.Limit)
			if err != nil {
				return "", err
			}
			b, _ := json.Marshal(messages)
			return string(b), nil
		},

		RegisterDeviceTools: registerDeviceTools,

		SpotifyPlay:       toolSpotifyPlay,
		SpotifySearch:     toolSpotifySearch,
		SpotifyNowPlaying: toolSpotifyNowPlaying,
		SpotifyDevices:    toolSpotifyDevices,
		SpotifyRecommend:  toolSpotifyRecommend,
		YouTubeSummary:    toolYouTubeSummary,
		PodcastList:       toolPodcastList,

		PdfRead:        toolPdfRead,
		DocSummarize:   toolDocSummarize,
		FileStore:      toolFileStore,
		FileList:       toolFileList,
		FileDuplicates: toolFileDuplicates,
		FileOrganize:   toolFileOrganize,
		DriveSearch:    toolDriveSearch,
		DriveUpload:    toolDriveUpload,
		DriveDownload:  toolDriveDownload,
		DropboxOp:      toolDropboxOp,

		OAuthStatus:    toolOAuthStatus,
		OAuthRequest:   toolOAuthRequest,
		OAuthAuthorize: toolOAuthAuthorize,

		ReminderSet:    toolReminderSet,
		ReminderList:   toolReminderList,
		ReminderCancel: toolReminderCancel,
	}
}
