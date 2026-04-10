package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"golang.org/x/net/proxy"
)

// ==== CONFIG CONSTANTS ====
const (
	ACCOUNTS_FILE        = "accounts.txt"
	LOG_FILE             = "found_ips.txt"
	BASE_URL             = "https://api.selectel.ru/vpc/resell/v2"
	MAX_ATTEMPTS         = 50000
	RETRY_DELAY_MS       = 1500
	CONSECUTIVE_ATTEMPTS = 10
	TIME_DELAY_SEC       = 330 // 5 минут
)

var REGIONS = []string{"ru-2", "ru-3", "ru-7"}

var TARGET_CIDRS = []string{
	"5.101.50.0/23", "5.178.85.0/24", "5.188.56.0/24", "5.188.112.0/22",
	"5.188.118.0/23", "5.188.158.0/23", "5.189.239.0/24", "31.41.157.0/24",
	"31.172.128.0/24", "31.184.211.0/24", "31.184.215.0/24", "31.184.218.0/24",
	"31.184.253.0/24", "31.184.254.0/24", "37.9.4.0/24", "37.9.13.0/24",
	"78.24.181.0/24", "80.93.187.0/24", "80.249.145.0/24", "80.249.146.0/23",
	"81.163.22.0/23", "82.202.192.0/19", "82.202.224.0/22", "82.202.228.0/24",
	"82.202.230.0/23", "82.202.233.0/24", "82.202.234.0/23", "82.202.236.0/22",
	"82.202.240.0/20", "84.38.181.0/24", "84.38.182.0/24", "84.38.185.0/24",
	"87.228.101.0/24", "178.72.0.0/22", "185.91.53.0/24", "185.91.54.0/24",
	"188.68.218.0/24",
}

type Account struct {
	Token     string
	ProjectID string
	ProxyURL  string
	ID        int
}

type FloatingIP struct {
	ID      string `json:"id"`
	Address string `json:"floating_ip_address"`
	Region  string `json:"region"`
}

type CreateResponse struct {
	FloatingIPs []FloatingIP `json:"floatingips"`
}

type ListResponse struct {
	FloatingIPs []FloatingIP `json:"floatingips"`
}

var fileMutex sync.Mutex

func main() {
	fmt.Println("🚀 Запуск мульти-аккаунтного бота Selectel...")

	accounts, err := loadAccounts(ACCOUNTS_FILE)
	if err != nil {
		fmt.Printf("❌ Ошибка чтения файла %s: %v\n", ACCOUNTS_FILE, err)
		os.Exit(1)
	}

	if len(accounts) == 0 {
		fmt.Println("⚠️ Список аккаунтов пуст.")
		os.Exit(1)
	}

	fmt.Printf("✅ Загружено аккаунтов: %d\n", len(accounts))
	fmt.Printf(" Логирование в: %s\n", LOG_FILE)
	fmt.Println(strings.Repeat("=", 50))

	var wg sync.WaitGroup

	for _, acc := range accounts {
		wg.Add(1)
		go func(account Account) {
			defer wg.Done()
			runAccountWorker(account)
		}(acc)
	}

	wg.Wait()
	fmt.Println("\n🏁 Все аккаунты завершили работу.")
}

func loadAccounts(filename string) ([]Account, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var accounts []Account
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			fmt.Printf("️ Пропущена строка %d: неверный формат\n", lineNum)
			continue
		}

		acc := Account{
			ID:        lineNum,
			Token:     strings.TrimSpace(parts[0]),
			ProjectID: strings.TrimSpace(parts[1]),
		}
		if len(parts) > 2 {
			acc.ProxyURL = strings.TrimSpace(parts[2])
		}

		accounts = append(accounts, acc)
	}

	return accounts, scanner.Err()
}

func runAccountWorker(acc Account) {
	fmt.Printf("\n>>> АККАУНТ #%d (Project: %s) ЗАПУЩЕН <<<\n", acc.ID, acc.ProjectID)

	client := resty.New().
		SetBaseURL(BASE_URL).
		SetHeader("X-Auth-Token", acc.Token).
		SetHeader("Content-Type", "application/json").
		SetTimeout(30 * time.Second)

	if acc.ProxyURL != "" {
		if err := setupProxy(client, acc.ProxyURL); err != nil {
			fmt.Printf("❌ Аккаунт #%d: Ошибка настройки прокси: %v\n", acc.ID, err)
			return
		}
	}

	foundCount := 0
	dynamicDelay := RETRY_DELAY_MS

	for attempt := 1; attempt <= MAX_ATTEMPTS; attempt++ {
		if attempt%CONSECUTIVE_ATTEMPTS == 0 {
			fmt.Printf("    Аккаунт #%d: Пауза %d сек...\n", acc.ID, TIME_DELAY_SEC)
			time.Sleep(time.Duration(TIME_DELAY_SEC) * time.Second)
		}

		region := REGIONS[time.Now().UnixNano()%int64(len(REGIONS))]

		ip, id, err := createFloatingIP(client, acc.ProjectID, region)
		if err != nil {
			if strings.Contains(err.Error(), "409") && strings.Contains(err.Error(), "quota_exceeded") {
				fmt.Printf("   ⚠️ Аккаунт #%d: Квота превышена в %s. Чистка...\n", acc.ID, region)
				cleanupRegion(client, acc.ProjectID, region)
				time.Sleep(2000 * time.Millisecond)
				continue
			}
			
			if strings.Contains(err.Error(), "API Error") {
				if strings.Contains(err.Error(), "429") {
					dynamicDelay += 1000
				}
				time.Sleep(time.Duration(dynamicDelay) * time.Millisecond)
				continue
			}
			
			time.Sleep(time.Duration(RETRY_DELAY_MS) * time.Millisecond)
			continue
		}

		now := time.Now().Format("15:04:05")
		
		matchedCidr := checkIpInAnyCidr(ip, TARGET_CIDRS)
		isFound := matchedCidr != ""

		var logEntry string
		
		// ИЗМЕНЕНИЕ ЗДЕСЬ: Добавляем [Аккаунт: #%d] во все записи
		if isFound {
			logEntry = fmt.Sprintf("[%s] ✅ НАЙДЕН: %s | Подсеть: %s | Регион: %s | Аккаунт: #%d\n", 
				now, ip, matchedCidr, region, acc.ID)
			foundCount++
			fmt.Printf("   [%s] ✅ Аккаунт #%d: IP %s попал в %s!\n", now, acc.ID, ip, matchedCidr)
		} else {
			// Теперь и обычные IP пишутся с номером аккаунта
			logEntry = fmt.Sprintf("%s [Регион: %s] [Аккаунт: #%d]\n", ip, region, acc.ID)
			deleteFloatingIP(client, id)
		}

		fileMutex.Lock()
		if err := appendToFile(LOG_FILE, logEntry); err != nil {
			fmt.Printf("   ⚠️ Ошибка записи в файл: %v\n", err)
		}
		fileMutex.Unlock()

		time.Sleep(time.Duration(RETRY_DELAY_MS) * time.Millisecond)
	}

	fmt.Printf(">>> АККАУНТ #%d ЗАВЕРШЕН. Найдено: %d <<<\n", acc.ID, foundCount)
}

func setupProxy(client *resty.Client, proxyURL string) error {
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return err
	}

	transport := &http.Transport{}

	if strings.HasPrefix(proxyURL, "socks5") {
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, nil, proxy.Direct)
		if err != nil {
			return err
		}
		transport.Dial = dialer.Dial
	} else {
		transport.Proxy = http.ProxyURL(parsedURL)
	}

	client.SetTransport(transport)
	return nil
}

func createFloatingIP(client *resty.Client, projectID, region string) (string, string, error) {
	body := map[string]interface{}{
		"floatingips": []map[string]interface{}{
			{"quantity": 1, "region": region},
		},
	}

	resp, err := client.R().
		SetBody(body).
		Post(fmt.Sprintf("/floatingips/projects/%s", projectID))

	if err != nil {
		return "", "", err
	}

	if resp.StatusCode() != 200 && resp.StatusCode() != 201 {
		return "", "", fmt.Errorf("API Error %d: %s", resp.StatusCode(), resp.String())
	}

	var result CreateResponse
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return "", "", fmt.Errorf("JSON parse error: %v", err)
	}

	if len(result.FloatingIPs) == 0 {
		return "", "", fmt.Errorf("No IP returned in response")
	}

	return result.FloatingIPs[0].Address, result.FloatingIPs[0].ID, nil
}

func deleteFloatingIP(client *resty.Client, id string) bool {
	resp, err := client.R().Delete(fmt.Sprintf("/floatingips/%s", id))
	if err != nil {
		return false
	}
	return resp.StatusCode() == 204 || resp.StatusCode() == 404
}

func cleanupRegion(client *resty.Client, projectID, region string) {
	resp, err := client.R().
		SetQueryParam("project_id", projectID).
		Get("/floatingips")

	if err != nil {
		return
	}

	var result ListResponse
	if err := json.Unmarshal(resp.Body(), &result); err != nil {
		return
	}

	for _, ipObj := range result.FloatingIPs {
		if ipObj.Region == region {
			deleteFloatingIP(client, ipObj.ID)
			time.Sleep(200 * time.Millisecond)
		}
	}
}

func checkIpInAnyCidr(ip string, cidrs []string) string {
	ipAddr := net.ParseIP(ip)
	if ipAddr == nil {
		return ""
	}

	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ipNet.Contains(ipAddr) {
			return cidr
		}
	}
	return ""
}

func appendToFile(filename, data string) error {
	f, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(data)
	return err
}