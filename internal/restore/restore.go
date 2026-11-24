// Package restore turns a PDF into a clean EPUB with the help of a
// language model: it reads scanned pages into text, tells headings,
// paragraphs and notes apart, gives the headings one style and builds
// the table of contents.
//
// The model only does what needs reading and judgement: transcribing a
// page, and standardising the headings. Opening the file, showing the
// pages, checking what the model hands back, putting the pages together
// and writing the book are done here, through the tools the model is
// given. A restore runs from start to end without anyone taking part.
// It begins with a sample of the first pages, after which the model
// says whether it expects to restore the book well; when it does not,
// the restore ends there.
package restore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/docker/docker-agent/pkg/agent"
	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/config/latest"
	"github.com/docker/docker-agent/pkg/environment"
	"github.com/docker/docker-agent/pkg/model/provider"
	"github.com/docker/docker-agent/pkg/model/provider/anthropic"
	"github.com/docker/docker-agent/pkg/model/provider/options"
	agentrt "github.com/docker/docker-agent/pkg/runtime"
	"github.com/docker/docker-agent/pkg/session"
	"github.com/docker/docker-agent/pkg/team"
	"github.com/docker/docker-agent/pkg/tools"

	"github.com/manzanit0/athenaeum/internal/meta"
)

// DefaultModel is the model a restore uses.
const DefaultModel = "claude-opus-5-5"

// How a restore is divided up.
const (
	// SamplePages is how many pages the model judges the book by.
	SamplePages = 10
	// batchPages is how many pages one session works through.
	batchPages = 10
	// workers is how many sessions run at once.
	workers = 3
	// maxTurns is the most turns the model gets in one session.
	maxTurns = 60
	// maxOutput is the most tokens the model writes in one turn.
	maxOutput = 32_000
	// unreadableShare is the share of the pages that may be stored in a
	// way that cannot be shown before the restore is not worth starting.
	unreadableShare = 0.2
)

// The stages of a restore.
const (
	// StageSample is the reading of the first pages.
	StageSample = "sample"
	// StagePages is the reading of the rest of the book.
	StagePages = "pages"
	// StageHeadings is the standardising of the headings.
	StageHeadings = "headings"
	// StageWriting is the writing of the book.
	StageWriting = "writing"
)

// ErrNothingToRead is returned for a PDF without a page that can be
// shown to the model.
var ErrNothingToRead = errors.New("the PDF has no page that can be read")

// Model is the language model a restore works with.
type Model = provider.Provider

// price is what a model costs, in dollars per million tokens.
type price struct {
	// input is the price of what the model reads.
	input float64
	// output is the price of what the model writes.
	output float64
}

// prices holds what the models cost. What is read from the cache costs
// a tenth of the input price, and what is written to it a quarter more.
var prices = map[string]price{
	"claude-opus-5-5": {input: 4, output: 20},
}

// Anthropic returns the model called name, reached with the API key
// key.
func Anthropic(ctx context.Context, key, name string) (Model, error) {
	limit := int64(maxOutput)
	return anthropic.NewClient(ctx,
		&latest.ModelConfig{Provider: "anthropic", Model: name, MaxTokens: &limit},
		environment.NewMapEnvProvider(map[string]string{"ANTHROPIC_API_KEY": key}),
		// The limit of the configuration alone does not reach the request.
		options.WithMaxTokens(limit))
}

// Options says what to restore and how.
type Options struct {
	// Source is the PDF to restore.
	Source string
	// Dest is where the EPUB is written.
	Dest string
	// Model is the language model to work with.
	Model Model
	// Metadata describes the book. Fields left empty fall back to what
	// the PDF says about itself.
	Metadata meta.Metadata
	// Cover is the path of a cover image to embed; empty means none.
	Cover string
	// ID identifies the book; empty means one is made up.
	ID string
	// Limit is the most the restore may cost, in dollars; zero means
	// there is no limit.
	Limit float64
	// Progress, when set, is told how far the restore has come.
	Progress func(Progress)
}

// Progress says how far a restore has come.
type Progress struct {
	// Stage is the stage the restore is in.
	Stage string `json:"stage"`
	// Pages is the number of pages of the book.
	Pages int `json:"pages"`
	// Read is the number of pages transcribed.
	Read int `json:"read"`
	// Cost is what the restore has cost so far, in dollars.
	Cost float64 `json:"cost"`
}

// Report says what a restore did.
type Report struct {
	// Survey counts the pages by what they hold.
	Survey
	// Sections is the number of chapters the book is divided into.
	Sections int `json:"sections"`
	// Headings is the number of headings.
	Headings int `json:"headings"`
	// HeadingsChanged is the number of headings whose capitals or level
	// were standardised.
	HeadingsChanged int `json:"headings_changed"`
	// NotesLinked is the number of references that lead to their note.
	NotesLinked int `json:"notes_linked"`
	// Doubts lists the passages that could not be read with certainty.
	Doubts []Doubt `json:"doubts"`
	// Warnings lists what did not go as it should.
	Warnings []string `json:"warnings"`
	// Model is the model that did the work.
	Model string `json:"model"`
	// InputTokens is how many tokens the model read.
	InputTokens int64 `json:"input_tokens"`
	// OutputTokens is how many tokens the model wrote.
	OutputTokens int64 `json:"output_tokens"`
	// Cost is what the restore cost, in dollars; zero when the price of
	// the model is not known.
	Cost float64 `json:"cost"`
}

// Declined is the error of a restore the model ended after the sample
// because it did not expect to restore the book well.
type Declined struct {
	// Reason is why, in the words of the model.
	Reason string
	// Cost is what the sample cost, in dollars.
	Cost float64
}

// Error says why the restore ended.
func (d *Declined) Error() string {
	reason := strings.TrimRight(d.Reason, ". ")
	if reason == "" {
		reason = "the model did not expect to restore the book well"
	}
	return fmt.Sprintf("the restore was ended after the first pages: %s", reason)
}

// instruction is what the model is told about its work in every
// session.
const instruction = `You restore printed books into clean digital text. A book reaches you as the pages of a PDF, a few at a time, through tools. You transcribe the pages; the program around you puts them together into an EPUB, builds its table of contents and links its notes. Nobody reads what you write in reply while you work, and nobody can answer a question: everything you do, you do by calling tools.

# Transcribing pages

` + ToolReadPages + ` shows you pages. A scanned page comes as a picture. A page that already has text comes as its lines, top to bottom, with what the page tells by its looks written out: **bold** and *italic* stretches are marked, a line that starts with a note such as "(size 18)" is set larger than the ordinary text, and an empty line stands where the page leaves a gap. Read these the way a reader reads the page: a short line set large or wholly in bold is usually a heading, a gap usually parts paragraphs or groups, and the notes themselves are never part of the text.

Call ` + ToolSavePage + ` once for every page you are asked for, with the text of the page as blocks in reading order. Save pages as you finish them, three to an answer at most: an answer that grows too long is cut off, and everything in it is lost.

The blocks:

- heading: the title of a part, chapter or section. Level 1 is a part or chapter, level 2 a section within it, level 3 a subsection. Number it as printed ("I.", "Chapter 3").
- paragraph: a paragraph of running text. Join its lines, and put a word that was hyphenated at the end of a line back together. When a page opens in the middle of a paragraph that began on the page before, set "continues" on that block.
- quote: a quotation set apart from the text, or verse.
- item: one item of a list, a table of contents or an index. Its level says how deep it is nested: 1 for an item of the list, 2 for an item of a list within that item. Keep the number or letter an item is printed with ("3.", "a)") and leave out a mere bullet.
- title, subtitle, author, detail, epigraph, imprint: what the title page of the book says, wherever in the opening pages it stands. A detail is a line about the author or the making of the book, such as an office the author holds, a translator, an editor or an edition. An epigraph is a quotation the book opens with; give its source as a second epigraph block. An imprint is a line about the publishing: the publisher, the place, the year.
- note: a footnote or endnote, with the number or sign it is referred to by as its marker. Where the running text refers to a note, write [^marker] at that place, for example [^3] or [^*].

Keep the emphasis of the book: mark italics as *italics* and bold as **bold** wherever the page sets words so, in paragraphs, quotes, items and notes alike. Only a heading or a line of the title page is not marked for being set in bold.

Be faithful to the words. The reader must get the words of the book and nothing else: keep the spelling, punctuation and wording exactly as printed, old or odd as they may be. Do not correct, modernise, translate, shorten or explain, and never write anything that is not on the page.

Capitals are not part of that. Printers set words in capitals for looks: on title pages, in headings, for the opening words of a chapter, in signatures, seals and closing lines, in the names of authors cited. Never carry that over. Write every word with the capitals the grammar of its language asks for and no others: a capital to open a sentence and for names, lower case for the rest, and abbreviations and Roman numerals as they are. This holds for every kind of block, the lines of the title page included. A word taken out of capitals gets the accent its capitals left out ("TENTACION" becomes "tentación").

Capitals the author wrote are part of the text, and stay. A character who shouts in a novel, a sign, a telegram or an inscription quoted in capitals, a word the author capitalised to make a point: these mean something, and lower case would lose it. The question is always who chose the capitals. The printer, for the look of the page: write them as grammar asks. The author, for what they say: keep them. When you cannot tell, keep them.

Be thoughtful about the form. You are making a well-made ebook, not a copy of how the pages happen to look, so give every piece of text the kind that says what it is:

- The program builds the first page of the ebook from the title, subtitle, author, detail, epigraph and imprint blocks, laid out the way a publisher lays out a title page. Mark what the opening pages print of these as such, whichever page and order they come in, and never as headings or paragraphs. Do not repeat them as headings. Write them with the capitals grammar asks for: the program itself shows the title in capitals.
- A list keeps its shape. When some entries stand out, in bold or apart, with others gathered under them, the entries that stand out are level 1 and those under them level 2. A printed table of contents is such a list: its parts or chapters at level 1, what is listed under each at level 2.
- A heading is what opens a part of the text that follows it. A line of a table of contents is an item, even when it has the words of a heading.
- Tell the divisions of the text from its decoration by reading it. The chapters of a book are the parts its text is really divided into, often only numbered ("I.", "II."): those are level 1 headings. A line that is merely set large or centred is not one: a salutation ("The bishop to his faithful"), a dedication, a motto, an epigraph, the opening words of a chapter in capitals, a signature. Give such a line the kind of what it is, a paragraph or a quote, and leave its words in the text.

Leave out what belongs to the page rather than the text: running headers and footers, page numbers, printer's marks, stamps and stains. Read a page set in columns one column after the other. Give the text of a table row by row as paragraphs, and the caption of a picture as a paragraph.

When you cannot read something with certainty, write your best reading in the text and list the passage under "doubts" with the reason. Never leave a gap or a placeholder, and never guess without saying so. Save a page with nothing to read on it with no blocks.

# The sample

The first pages you are given are a sample. Once they are saved, call ` + ToolJudgeSample + ` and say honestly whether you expect to restore the rest of the book well. Say you do not when the text cannot be read reliably (too faint, cut off, skewed, handwritten), when you cannot transcribe its script faithfully, when the pages cannot be shown, or when the book is mostly tables, formulas, music or pictures that would lose their meaning as plain text. Pages with little on them, as a book often starts with, are no reason by themselves. If the first pages already show that the book cannot be restored well, say so at once without saving the rest of the sample: a restore that ends here costs the owner little, one that goes on and fails costs them the whole book.

# Standardising headings and capitals

Once every page is transcribed you are shown the book as a whole, which no one page showed you: its headings, each with how the text under it begins and how long that text is, the lines of its title page, and the stretches of words still set in capitals. This is where the book gets one shape.

Call ` + ToolSetHeadings + ` once, with every heading. Decide the levels from the structure of the whole book, not from how each page looked. The divisions the text is really cut into are level 1, in every chapter alike, even when they are only numbered and a grander line stands above them. What stands above the first division without being one, such as a salutation, a dedication or a motto, and what slipped through, such as a running header, is not a heading: give it level 0 and it becomes ordinary text. Sections within a chapter are level 2. Give every heading the capitals of a sentence in its language.

Call ` + ToolSetCapitals + ` once, with every line and stretch listed, each written with the capitals grammar asks for. A stretch that is rightly in capitals goes back as it is: an abbreviation, a Roman numeral, and above all capitals the author wrote, such as a character shouting or a sign quoted. Each stretch comes with the text it stands in, so read it there before you decide. Change nothing else about the words; accents that capitals left out may be put back.

When your work is done, reply with the one word "Done".`

// run is one restore under way.
type run struct {
	// opts says what to restore and how.
	opts Options
	// source is the PDF.
	source *source
	// draft is the book as transcribed so far.
	draft *draft
	// stop ends the restore with a cause.
	stop context.CancelCauseFunc
	// mu guards the fields below.
	mu sync.Mutex
	// stage is the stage the restore is in.
	stage string
	// input is how many tokens the model has read.
	input int64
	// output is how many tokens the model has written.
	output int64
	// cost is what the restore has cost, in dollars.
	cost float64
}

// Inspect counts the pages of the PDF at path by what they hold.
func Inspect(path string) (Survey, error) {
	src, err := openSource(path)
	if err != nil {
		return Survey{}, err
	}
	defer src.close()
	return src.survey(), nil
}

// Restore turns the PDF at opts.Source into an EPUB at opts.Dest.
func Restore(ctx context.Context, opts Options) (*Report, error) {
	src, err := openSource(opts.Source)
	if err != nil {
		return nil, err
	}
	defer src.close()
	survey := src.survey()
	readable := survey.Scans + survey.Text
	if readable == 0 {
		return nil, ErrNothingToRead
	}
	if float64(survey.Unreadable) > unreadableShare*float64(survey.Pages) {
		return nil, fmt.Errorf("%w: %d of its %d pages are scans stored in a way that cannot be shown yet",
			ErrNothingToRead, survey.Unreadable, survey.Pages)
	}
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	r := &run{opts: opts, source: src, draft: newDraft(), stop: stop}
	if err := r.transcribe(ctx, survey.Pages); err != nil {
		return nil, r.cause(ctx, err)
	}
	r.draft.assemble()
	report := &Report{Survey: survey, Warnings: []string{}, Model: opts.Model.ID().Model}
	if len(r.draft.elements) == 0 {
		return nil, fmt.Errorf("%w: nothing was transcribed", ErrNothingToRead)
	}
	if err := r.standardise(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, r.cause(ctx, err)
		}
		report.Warnings = append(report.Warnings, fmt.Sprintf("The headings were left as printed: %v.", err))
	}
	r.enter(StageWriting)
	info := src.info()
	details := meta.Fill(opts.Metadata, meta.Metadata{
		Title: info.Title, Authors: authors(info.Author), Language: info.Language,
	})
	result, err := writeBook(opts.Dest, r.draft.elements, details, opts.ID, opts.Cover)
	if err != nil {
		return nil, err
	}
	report.Sections, report.Headings, report.NotesLinked = result.sections, result.headings, result.linked
	report.HeadingsChanged = r.draft.changed
	report.Doubts = append([]Doubt{}, r.draft.doubts...)
	report.InputTokens, report.OutputTokens, report.Cost = r.input, r.output, r.cost
	return report, nil
}

// authors returns the author named by a PDF as a list.
func authors(name string) []string {
	if name = strings.TrimSpace(name); name != "" {
		return []string{name}
	}
	return nil
}

// cause returns why the restore ended: the cause it was stopped with,
// when there is one, and err otherwise.
func (r *run) cause(ctx context.Context, err error) error {
	if cause := context.Cause(ctx); cause != nil && !errors.Is(cause, context.Canceled) {
		return cause
	}
	return err
}

// enter moves the restore to the given stage.
func (r *run) enter(stage string) {
	r.mu.Lock()
	r.stage = stage
	r.mu.Unlock()
	r.report()
}

// report tells whoever follows the restore how far it has come.
func (r *run) report() {
	if r.opts.Progress == nil {
		return
	}
	r.mu.Lock()
	progress := Progress{Stage: r.stage, Pages: len(r.source.kinds), Cost: r.cost}
	r.mu.Unlock()
	progress.Read = r.draft.saved()
	r.opts.Progress(progress)
}

// spend counts what one answer of the model cost, and ends the restore
// when it has cost more than it may.
func (r *run) spend(usage *agentrt.MessageUsage) {
	if usage == nil {
		return
	}
	r.mu.Lock()
	r.input += usage.InputTokens + usage.CachedInputTokens + usage.CacheWriteTokens
	r.output += usage.OutputTokens
	if p, ok := prices[r.opts.Model.ID().Model]; ok {
		r.cost += (float64(usage.InputTokens)*p.input + float64(usage.CachedInputTokens)*p.input*0.1 +
			float64(usage.CacheWriteTokens)*p.input*1.25 + float64(usage.OutputTokens)*p.output) / 1e6
	}
	over := r.opts.Limit > 0 && r.cost > r.opts.Limit
	cost := r.cost
	r.mu.Unlock()
	if over {
		r.stop(fmt.Errorf("the restore was stopped because it had cost $%.2f, more than the $%.2f it was allowed", cost, r.opts.Limit))
	}
	r.report()
}

// ending says how a session of the model came to its end.
type ending struct {
	// err is the error the session ran into, if any.
	err error
	// finish is why the model ended its last answer.
	finish chat.FinishReason
	// turns reports whether the model used up its turns.
	turns bool
	// refused is the last thing a tool turned down.
	refused string
	// said is what the model wrote in its last answer.
	said string
}

// why words how the session ended for the owner of the book, for when
// it ended without the work being done.
func (e ending) why() error {
	switch {
	case e.err != nil:
		return e.err
	case e.finish == chat.FinishReasonLength:
		return errors.New("the answer of the model was cut off for being too long, and what it held was lost")
	case e.finish == chat.FinishReasonRefusal:
		return errors.New("the model declined to go on")
	case e.turns:
		return fmt.Errorf("the model used up its %d turns", maxTurns)
	case e.refused != "":
		return fmt.Errorf("the model gave up after being told: %s", quote(e.refused))
	case strings.TrimSpace(e.said) != "":
		return fmt.Errorf("the model stopped before it had saved them, saying: %s", quote(e.said))
	}
	return errors.New("the model stopped before it had saved them, without saying why")
}

// quote puts text, shortened and on one line, in quotation marks.
func quote(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 240 {
		text = string(r[:240]) + "…"
	}
	return "“" + text + "”"
}

// session has the model work on prompt with the given tools until it
// stops, and returns how it came to its end.
func (r *run) session(ctx context.Context, prompt string, kit []tools.Tool) ending {
	worker := agent.New("restorer", instruction,
		agent.WithModel(r.opts.Model),
		agent.WithTools(kit...),
		agent.WithMaxIterations(maxTurns),
	)
	rt, err := agentrt.New(ctx, team.New(team.WithAgents(worker)),
		agentrt.WithSessionStore(session.NewInMemorySessionStore()))
	if err != nil {
		return ending{err: err}
	}
	defer rt.Close()
	sess := session.New(
		session.WithUserMessage(prompt),
		session.WithTitle("Restore"),
		session.WithToolsApproved(true),
	)
	var end ending
	for event := range rt.RunStream(ctx, sess) {
		switch e := event.(type) {
		case *agentrt.ErrorEvent:
			end.err = errors.New(e.Error)
		case *agentrt.TokenUsageEvent:
			if e.Usage != nil && !e.Usage.SnapshotOnly && e.Usage.LastMessage != nil {
				r.spend(e.Usage.LastMessage)
				end.finish, end.said = e.Usage.LastMessage.FinishReason, ""
			}
		case *agentrt.AgentChoiceEvent:
			end.said += e.Content
		case *agentrt.ToolCallResponseEvent:
			end.refused = ""
			if e.Result != nil && e.Result.IsError {
				end.refused = e.Result.Output
			}
		case *agentrt.MaxIterationsReachedEvent:
			end.turns = true
		case *agentrt.StreamStoppedEvent:
			if e.FinishReason != "" {
				end.finish = e.FinishReason
			}
		}
	}
	if err := ctx.Err(); err != nil {
		end.err = err
	}
	if end.err != nil || end.finish != chat.FinishReasonStop {
		slog.Warn("a restore session ended early", "prompt", quote(prompt), "finish", end.finish,
			"error", end.err, "turns", end.turns, "refused", end.refused, "said", quote(end.said))
	}
	return end
}

// work has the model transcribe the pages of b. When pages are left
// unsaved it is asked once more for those, and what is still missing
// after that fails the restore.
func (r *run) work(ctx context.Context, b batch, prompt string) error {
	end := r.session(ctx, prompt, r.pageTools(b))
	if err := ctx.Err(); err != nil {
		return err
	}
	if v := r.draft.judged(); b.sample && v != nil && !v.good {
		return nil
	}
	left := r.draft.missing(b.first, b.last)
	if len(left) > 0 {
		again := fmt.Sprintf("Pages %s of the book are not saved yet. Read them, transcribe them and save each of them, "+
			"one page to an answer: read a page, save it, and only then go on to the next.", list(left))
		end = r.session(ctx, again, r.pageTools(batch{first: b.first, last: b.last}))
		if err := ctx.Err(); err != nil {
			return err
		}
		left = r.draft.missing(b.first, b.last)
	}
	if len(left) == 0 {
		return nil
	}
	return fmt.Errorf("pages %s could not be transcribed: %w", list(left), end.why())
}

// transcribe reads every page of the book: the sample first, and when
// the model expects to restore the book well, the rest.
func (r *run) transcribe(ctx context.Context, pages int) error {
	r.enter(StageSample)
	sample := batch{first: 1, last: min(SamplePages, pages), sample: true}
	prompt := fmt.Sprintf("This is the sample: pages %d to %d of the %d pages of %s. Read them, transcribe them and save each of them. "+
		"Then call %s to say whether you expect to restore the rest of the book well.",
		sample.first, sample.last, pages, r.title(), ToolJudgeSample)
	if err := r.work(ctx, sample, prompt); err != nil {
		return err
	}
	if v := r.draft.judged(); v != nil && !v.good {
		return &Declined{Reason: v.reason, Cost: r.cost}
	}
	r.enter(StagePages)
	var batches []batch
	for first := sample.last + 1; first <= pages; first += batchPages {
		batches = append(batches, batch{first: first, last: min(first+batchPages-1, pages)})
	}
	var (
		wg     sync.WaitGroup
		once   sync.Once
		failed error
		queue  = make(chan batch)
	)
	for range min(workers, len(batches)) {
		wg.Go(func() {
			for b := range queue {
				prompt := fmt.Sprintf("Read pages %d to %d of the %d pages of %s, transcribe them and save each of them. "+
					"The sample has been judged already.", b.first, b.last, pages, r.title())
				if err := r.work(ctx, b, prompt); err != nil {
					once.Do(func() {
						failed = err
						r.stop(err)
					})
				}
			}
		})
	}
	for _, b := range batches {
		if ctx.Err() != nil {
			break
		}
		queue <- b
	}
	close(queue)
	wg.Wait()
	if failed != nil {
		return failed
	}
	return ctx.Err()
}

// around returns the part of text that surrounds the stretch found in
// it, for the model to read the stretch in its sentence.
func around(text, found string) string {
	const margin = 80
	at := strings.Index(text, found)
	if at < 0 {
		return clipTo(text, 2*margin)
	}
	before, after := []rune(text[:at]), []rune(text[at+len(found):])
	lead, tail := "", ""
	if len(before) > margin {
		before, lead = before[len(before)-margin:], "…"
	}
	if len(after) > margin {
		after, tail = after[:margin], "…"
	}
	return lead + string(before) + found + string(after) + tail
}

// title names the book for the model.
func (r *run) title() string {
	title := r.opts.Metadata.Title
	if title == "" {
		title = r.source.info().Title
	}
	if title == "" {
		return "the book"
	}
	if len(r.opts.Metadata.Authors) > 0 {
		return fmt.Sprintf("%q by %s", title, strings.Join(r.opts.Metadata.Authors, ", "))
	}
	return fmt.Sprintf("%q", title)
}

// standardise has the model give the book one shape: its headings the
// levels the structure of the whole asks for, and its headings, title
// page and text the capitals grammar asks for.
func (r *run) standardise(ctx context.Context) error {
	headings, stretches := r.draft.headings(), r.draft.stretches()
	if len(headings) == 0 && len(stretches) == 0 {
		return nil
	}
	r.enter(StageHeadings)
	var sb strings.Builder
	fmt.Fprintf(&sb, "Every page of %s is transcribed.\n", r.title())
	if len(headings) > 0 {
		fmt.Fprintf(&sb, "\nThese are its %d headings in reading order, as identifier | page | level | text, "+
			"each followed by how the text under it begins and how long that text is:\n\n", len(headings))
		for _, h := range headings {
			fmt.Fprintf(&sb, "%s | %d | %d | %s\n", h.id, h.page, h.Level, h.Text)
			if h.length == 0 {
				sb.WriteString("    no text before the next heading\n")
			} else {
				fmt.Fprintf(&sb, "    %d characters, beginning: %s\n", h.length, h.opens)
			}
		}
		fmt.Fprintf(&sb, "\nCall %s once with every one of them.\n", ToolSetHeadings)
	}
	if len(stretches) > 0 {
		fmt.Fprintf(&sb, "\nThese are the lines of its title page and the stretches of its text set in capitals, "+
			"as identifier | page | text, a stretch followed by the text it stands in:\n\n")
		for _, s := range stretches {
			fmt.Fprintf(&sb, "%s | %d | %s\n", s.id, s.page, s.text)
			if !s.whole {
				fmt.Fprintf(&sb, "    in: %s\n", around(s.within, s.text))
			}
		}
		fmt.Fprintf(&sb, "\nCall %s once with every one of them.\n", ToolSetCapitals)
	}
	end := r.session(ctx, sb.String(), r.headingTools(stretches))
	if end.err != nil {
		return end.err
	}
	if end.finish == chat.FinishReasonLength {
		return end.why()
	}
	return nil
}
