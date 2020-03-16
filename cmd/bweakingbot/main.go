package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	bluemonday "github.com/microcosm-cc/bluemonday"
	gofeed "github.com/mmcdole/gofeed"
	twitterscraper "github.com/n0madic/twitter-scraper"
	gowo "github.com/rewbycraft/gOwO/pkg/gowo"
	cron "github.com/robfig/cron/v3"
	xurls "mvdan.cc/xurls/v2"
)

type DiscordWebhookEmbedAuthor struct {
	Name string `json:"name"`
	Url  string `json:"url"`
	Icon string `json:"icon_url"`
}

type DiscordWebhookEmbed struct {
	Title       string                    `json:"title"`
	Description string                    `json:"description"`
	Url         string                    `json:"url"`
	Timestamp   string                    `json:"timestamp"`
	Author      DiscordWebhookEmbedAuthor `json:"author"`
}

type DiscordWebhook struct {
	Content string                `json:"content"`
	Name    string                `json:"username"`
	Avatar  string                `json:"avatar_url"`
	Embeds  []DiscordWebhookEmbed `json:"embeds"`
}

var lastActualTime = make(map[string]time.Time)
var webhookUrl = flag.String("webhook", "", "Webhook URL")

func (w DiscordWebhook) Post() {
	if webhookUrl == nil || *webhookUrl == "" {
		log.Fatalf("Cannot post webhook to empty URL!")
	}
	u, err := url.Parse(*webhookUrl)
	if err != nil {
		log.Fatalf("Cannot parse webhook URL: %+v", err)
	}

	q, _ := url.ParseQuery(u.RawQuery)

	q.Add("wait", "true")

	u.RawQuery = q.Encode()

	DOD, _ := json.Marshal(w)
	resp, err := http.Post(u.String(), "application/json", bytes.NewReader(DOD))
	if err != nil {
		log.Printf("Failure posting webhook: %+v", err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			b, _ := ioutil.ReadAll(resp.Body)
			log.Printf("Failure posting webhook to %s:\n%+v\n%s", u.String(), resp, string(b))
		}
	}
}

func postTweet(tweet *twitterscraper.Result, account string, avatar string) {
	log.Printf("New tweet: %s - %s @ %s", tweet.Text, tweet.PermanentURL, tweet.TimeParsed.Format(time.UnixDate))
	urlRegex := xurls.Strict()
	hook := DiscordWebhook{}
	hook.Name = fmt.Sprintf("@%s", account)
	hook.Avatar = avatar
	hook.Content = fmt.Sprintf("New post by @%s!", account)
	hook.Embeds = []DiscordWebhookEmbed{DiscordWebhookEmbed{}}
	hook.Embeds[0].Title = urlRegex.ReplaceAllString(tweet.Text, "")
	hook.Embeds[0].Author.Name = hook.Name
	hook.Embeds[0].Author.Icon = hook.Avatar
	hook.Embeds[0].Author.Url = fmt.Sprintf("https://twitter.com/%s", url.QueryEscape(account))

	urls := urlRegex.FindAllString(tweet.Text, -1)

	if len(urls) == 0 {
		hook.Embeds[0].Url = tweet.PermanentURL
	} else {
		hook.Embeds[0].Url = urls[0]
	}

	for i, url := range urls {
		hook.Embeds[0].Description = fmt.Sprintf("%sUrl[%d] = %s\n", hook.Embeds[0].Description, i, url)
	}
	hook.Embeds[0].Description = fmt.Sprintf("%s\nTwitter url = %s\n", hook.Embeds[0].Description, tweet.PermanentURL)
	hook.Embeds[0].Timestamp = tweet.TimeParsed.Format(time.RFC3339)

	hook.Post()
}

func postFeedItem(item *gofeed.Item, feed *gofeed.Feed) {
	log.Printf("New RSS post: %s @ %s", item.Title, item.PublishedParsed.Format(time.UnixDate))
	p := bluemonday.StrictPolicy()

	hook := DiscordWebhook{}
	hook.Name = feed.Title
	hook.Avatar = feed.Image.URL
	hook.Content = fmt.Sprintf("New post by %s!", feed.Title)
	hook.Embeds = []DiscordWebhookEmbed{DiscordWebhookEmbed{}}
	hook.Embeds[0].Title = gowo.OwOify(item.Title, true, true)
	hook.Embeds[0].Description = gowo.OwOify(p.Sanitize(item.Description), false, false)
	hook.Embeds[0].Url = item.Link
	hook.Embeds[0].Author.Name = hook.Name
	hook.Embeds[0].Author.Icon = hook.Avatar
	hook.Embeds[0].Author.Url = feed.Link
	hook.Embeds[0].Timestamp = item.PublishedParsed.Format(time.RFC3339)

	hook.Post()

}

func pollTwitter(name string, avatar string) {

	if _, ok := lastActualTime[name]; !ok {
		lastActualTime[name] = time.Now()
	}

	log.Printf("Polling @%s for tweets sent after %s...", name, lastActualTime[name].Format(time.UnixDate))
	var lastFoundTweetTime *time.Time = nil
	for tweet := range twitterscraper.GetTweets(name, 1) {
		if tweet.Error != nil {
			log.Println(tweet.Error)
			continue
		}
		if tweet.TimeParsed.After(lastActualTime[name]) {

			postTweet(tweet, name, avatar)

			if lastFoundTweetTime == nil || tweet.TimeParsed.After(*lastFoundTweetTime) {
				lastFoundTweetTime = &tweet.TimeParsed
			}
		}
	}
	if lastFoundTweetTime != nil {
		lastActualTime[name] = *lastFoundTweetTime
	}
}

func pollRSSFeed(url string) {

	if _, ok := lastActualTime[url]; !ok {
		lastActualTime[url] = time.Now()
	}

	log.Printf("Polling %s for posts after %s...", url, lastActualTime[url].Format(time.UnixDate))
	fp := gofeed.NewParser()
	feed, err := fp.ParseURL(url)
	if err != nil {
		log.Printf("Error polling %s: %+v", url, err)
		return
	}

	var lastFoundTime *time.Time = nil
	for _, item := range feed.Items {
		if item.PublishedParsed.After(lastActualTime[url]) {

			postFeedItem(item, feed)

			if lastFoundTime == nil || item.PublishedParsed.After(*lastFoundTime) {
				lastFoundTime = item.PublishedParsed
			}
		}
	}
	if lastFoundTime != nil {
		lastActualTime[url] = *lastFoundTime
	}
}

func pollTwitterAccounts() {
	accounts := []string{
		"BBCBweaking",
	}
	avatars := []string{
		"https://pbs.twimg.com/profile_images/1114682314729099265/s2UTPyit_200x200.png",
	}

	for i, acct := range accounts {
		pollTwitter(acct, avatars[i])
	}
}

func pollRSSFeeds() {
	feeds := []string{
		"https://www.dutchnews.nl/feed/",
	}

	for _, feed := range feeds {
		pollRSSFeed(feed)
	}
}

func main() {
	flag.Parse()
	c := cron.New(
		cron.WithSeconds(),
		cron.WithLogger(
			cron.VerbosePrintfLogger(log.New(os.Stdout, "", log.LstdFlags)),
		),
	)
	c.AddFunc("0 * * * * *", pollTwitterAccounts)
	c.AddFunc("0 * * * * *", pollRSSFeeds)
	c.Run()
}
