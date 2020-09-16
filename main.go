package main

import (
    "bufio"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "os"
    "strings"
    "sync"
    "time"

    "github.com/cenkalti/backoff"

    h "github.com/gadelkareem/go-helpers"
)

const (
    testURl        = "http://httpbin.org/ip"
    maxConcurrency = 100
)

var (
    currentIP string
)

func main() {
    setCurrentIP()

    path := os.Args[1]
    list, err := list(path)
    h.PanicOnError(err)
    err = writeFile("proxies.txt", list)
    h.PanicOnError(err)
    fmt.Printf("%d proxies written to proxies.txt\n", len(list))
}

func list(path string) ([]string, error) {
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    scanner := bufio.NewScanner(f)

    _, err = h.LiftRLimits()
    h.PanicOnError(err)

    var pSync sync.Map
    wg := h.NewWgExec(maxConcurrency)
    var l string
    for scanner.Scan() {
        l = scanner.Text()
        if l == "" {
            continue
        }
        wg.Run(func(p ...interface{}) {
            l := p[0].(string)
            err := testProxy(l)
            if err != nil {
                fmt.Fprintf(os.Stderr, err.Error()+"\n")
                return
            }
            fmt.Printf("Proxy %s passed\n", l)
            pSync.Store(l, 1)
        }, l)

    }
    wg.Wait()

    var proxies []string
    pSync.Range(func(key, value interface{}) bool {
        proxies = append(proxies, key.(string))
        return true
    })

    return proxies, nil
}

func testProxy(l string) error {
    u, err := url.Parse(fmt.Sprintf("http://%s", l))
    if err != nil {
        return err
    }
    transport := &http.Transport{Proxy: http.ProxyURL(u)}
    c := &http.Client{Transport: transport}
    c.Timeout = 60 * time.Second

    r, err := retryRequest(
        c,
        http.MethodGet,
        testURl,
        "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/58.0.3029.81 Safari/537.36",
        nil,
    )
    if err != nil {
        return err
    }
    defer r.Body.Close()

    if r.StatusCode != 200 {
        return fmt.Errorf("invalid Status Code: %d", r.StatusCode)
    }

    ips, origin, err := readJson(r.Body)
    if err != nil {
        return err
    }

    ip := ips[0]
    if !h.IsValidIp(ip) {
        return fmt.Errorf("invalid IP: %s", ip)
    }
    if strings.Contains(origin, currentIP) {
        return fmt.Errorf("not annonymous proxy IP: %s for line %s", origin, l)
    }

    return nil
}

func retryRequest(cl *http.Client, method, u, useragent string, body io.Reader) (resp *http.Response, err error) {
    backoff.Retry(func() error {
        resp, err = request(cl, method, u, useragent, body)
        if resp != nil && resp.StatusCode >= http.StatusTooManyRequests {
            return errors.New("try again")
        }
        return nil
    }, backoff.NewExponentialBackOff())
    return
}

func request(cl *http.Client, method, u, useragent string, body io.Reader) (*http.Response, error) {
    r, err := http.NewRequest(method, u, body)
    if err != nil {
        return nil, err
    }
    r.Header.Set("User-Agent", useragent)

    resp, err := cl.Do(r)
    if err != nil {
        return nil, err
    }

    if resp.StatusCode >= http.StatusBadRequest {
        return nil, fmt.Errorf("error response: %s", resp.Body)
    }

    return resp, nil
}

func setCurrentIP() {
    r, err := h.GetResponse(testURl)
    h.PanicOnError(err)
    ips, _, err := readJson(r.Body)
    h.PanicOnError(err)
    currentIP = ips[0]
    fmt.Printf("Current IP: %s\n", currentIP)
}

func writeFile(path string, proxies []string) error {
    f, err := os.Create(path)
    if err != nil {
        return err
    }
    defer f.Close()
    for _, v := range proxies {
        fmt.Fprintln(f, v)
    }
    return nil
}

func readJson(b io.Reader) ([]string, string, error) {
    t := struct {
        Origin string
    }{}
    err := json.NewDecoder(b).Decode(&t)
    if err != nil {
        return nil, t.Origin, err
    }
    ips := strings.Split(t.Origin, ",")
    if len(ips) < 1 {
        return nil, t.Origin, fmt.Errorf("no IPs found: %s", t.Origin)
    }
    return ips, t.Origin, nil
}
