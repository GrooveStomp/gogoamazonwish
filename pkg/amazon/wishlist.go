package amazon

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/gocolly/colly"
	"github.com/gocolly/colly/extensions"
	"github.com/gocolly/colly/proxy"
)

const (
	robotMessage = "we just need to make sure you're not a robot"
	cachePath    = "./cache"
	proxyPrefix  = "socks5://"
)

// Wishlist represents an Amazon wishlist of products.
type Wishlist struct {
	// DebugMode specifies whether messages should be logged to STDOUT about
	// what's going on, as well as if the HTML source of the wishlist should
	// be saved to files.
	DebugMode bool

	// CacheResults determines whether responses from Amazon should be cached.
	CacheResults bool

	proxyURLs []string
	urls      []string
	id        string
	items     map[string]*Item
}

// NewWishlist constructs an Amazon wishlist for the given URL.
func NewWishlist(urlStr string) (*Wishlist, error) {
	uri, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	if !uri.IsAbs() {
		return nil, fmt.Errorf("URL '%s' is not an absolute URL to an Amazon wishlist",
			urlStr)
	}

	if !strings.Contains(strings.ToLower(uri.Hostname()), "amazon") {
		return nil, fmt.Errorf("URL '%s' does not look like an Amazon wishlist URL",
			urlStr)
	}

	pathParts := strings.Split(uri.EscapedPath(), "/")
	id := pathParts[len(pathParts)-1]
	return NewWishlistFromID(id)
}

// NewWishlistFromID constructs an Amazon wishlist for the given wishlist ID.
func NewWishlistFromID(id string) (*Wishlist, error) {
	if len(id) < 1 {
		return nil, fmt.Errorf("ID '%s' does not look like an Amazon wishlist ID", id)
	}

	url := fmt.Sprintf("https://www.amazon.com/hz/wishlist/ls/%s?reveal=unpurchased&sort=date&layout=standard&viewType=list&filter=DEFAULT&type=wishlist", id)
	return &Wishlist{
		DebugMode:    false,
		CacheResults: true,
		urls:         []string{url},
		id:           id,
		items:        map[string]*Item{},
		proxyURLs:    []string{},
	}, nil
}

// SetProxyURLs specifies URLs of proxies to use when accessing Amazon. May
// be useful if you're getting an error about Amazon thinking you're a bot.
func (w *Wishlist) SetProxyURLs(urls ...string) {
	w.proxyURLs = make([]string, len(urls))
	for i, url := range urls {
		if strings.HasPrefix(url, proxyPrefix) {
			w.proxyURLs[i] = url
		} else {
			w.proxyURLs[i] = fmt.Sprintf("%s%s", proxyPrefix, url)
		}
	}
}

// Items returns a map of the products on the wishlist, where keys are
// the product IDs and the values are the products.
func (w *Wishlist) Items() (map[string]*Item, error) {
	options := []func(*colly.Collector){colly.Async(true)}
	if w.CacheResults {
		if w.DebugMode {
			fmt.Println("Caching Amazon responses in", cachePath)
		}
		options = append(options, colly.CacheDir(cachePath))
	}
	c := colly.NewCollector(options...)
	defer c.Wait()

	extensions.RandomUserAgent(c)
	c.Limit(&colly.LimitRule{
		RandomDelay: 2 * time.Second,
		Parallelism: 4,
	})

	if len(w.proxyURLs) > 0 {
		w.applyProxies(c)
	}

	c.OnRequest(func(r *colly.Request) {
		if w.DebugMode {
			fmt.Println("Using User-Agent", r.Headers.Get("User-Agent"))
		}
		r.Headers.Set("cookie", "i18n-prefs=USD")
	})

	c.OnResponse(func(r *colly.Response) {
		if w.DebugMode {
			fmt.Printf("Status %d\n", r.StatusCode)
		}

		if strings.Contains(string(r.Body), robotMessage) {
			log.Fatalln("Error: Amazon is not showing the wishlist because it thinks I'm a robot :(")
		}

		if w.DebugMode {
			filename := fmt.Sprintf("wishlist-%s-%s.html", w.id, r.FileName())
			fmt.Printf("Saving wishlist HTML source to %s...\n", filename)
			if err := r.Save(filename); err != nil {
				log.Println("Error: failed to save wishlist HTML to file")
				log.Fatalln(err)
			}
		}
	})

	c.OnHTML("ul li", w.onListItem)

	c.OnHTML("a.wl-see-more", func(link *colly.HTMLElement) {
		w.onLoadMoreLink(c, link)
	})

	c.OnError(func(r *colly.Response, e error) {
		fmt.Printf("Error: status %d\n", r.StatusCode)
		log.Fatalln(e)
	})

	if w.DebugMode {
		fmt.Println("Using URL", w.urls[0])
	}

	if err := c.Visit(w.urls[0]); err != nil {
		return nil, err
	}

	return w.items, nil
}

func (w *Wishlist) String() string {
	return strings.Join(w.urls, ", ")
}

func (w *Wishlist) onLoadMoreLink(c *colly.Collector, link *colly.HTMLElement) {
	relativeURL := link.Attr("href")
	if len(relativeURL) < 1 {
		return
	}

	nextPageURL := link.Request.AbsoluteURL(relativeURL)
	w.urls = append(w.urls, nextPageURL)

	if w.DebugMode {
		fmt.Println("Found URL to next page", nextPageURL)
	}

	c.Visit(nextPageURL)
}

func (w *Wishlist) onListItem(listItem *colly.HTMLElement) {
	id := listItem.Attr("data-itemid")
	if len(id) > 0 {
		listItem.ForEach("a", func(index int, link *colly.HTMLElement) {
			w.onListItemLink(id, link)
		})
		listItem.ForEach(".a-price", func(index int, priceEl *colly.HTMLElement) {
			w.onListItemPrice(id, priceEl)
		})
		listItem.ForEach(".dateAddedText", func(index int, dateEl *colly.HTMLElement) {
			w.onListItemDateAdded(id, dateEl)
		})
	}
}

func (w *Wishlist) onListItemLink(id string, link *colly.HTMLElement) {
	title := link.Attr("title")
	if len(title) < 1 {
		return
	}

	relativeURL := link.Attr("href")
	if len(relativeURL) < 1 {
		return
	}

	w.items[id] = &Item{
		DirectURL: link.Request.AbsoluteURL(relativeURL),
		Name:      title,
		ID:        id,
	}
}

func (w *Wishlist) onListItemPrice(id string, priceEl *colly.HTMLElement) {
	item := w.items[id]
	if item == nil {
		return
	}

	item.Price = priceEl.ChildText(".a-offscreen")
}

func (w *Wishlist) onListItemDateAdded(id string, dateEl *colly.HTMLElement) {
	item := w.items[id]
	if item == nil {
		return
	}

	childText := dateEl.ChildText("span")
	if len(childText) < 1 {
		return
	}

	lines := strings.Split(childText, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Added ") {
			addedText := strings.Split(line, "Added ")
			if len(addedText) >= 2 {
				item.DateAdded = addedText[1]
				break
			}
		}
	}
}

func (w *Wishlist) applyProxies(c *colly.Collector) error {
	if w.DebugMode {
		fmt.Printf("Using proxies: %v\n", w.proxyURLs)
	}

	proxySwitcher, err := proxy.RoundRobinProxySwitcher(w.proxyURLs...)
	if err != nil {
		return err
	}

	c.SetProxyFunc(proxySwitcher)

	return nil
}
