package main

import (
	"fmt"
	"image"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/MichaelS11/go-dht"

	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/devices/v3/ssd1306"
	"periph.io/x/devices/v3/ssd1306/image1bit"
	"periph.io/x/host/v3"
)

// 全域變數儲存配置狀態
var (
	// 使用互斥鎖保護
	configMutex sync.RWMutex
	envConfig   map[string]string

	showLOGO      bool
	showDHT       bool
	DHTType       string
	DHTPin        string
	onLoop        bool
	sleepTime     time.Duration
	originalSleep time.Duration
	firstRun      bool
	stepBy        int
	stepEnd       int

	button1Pin    gpio.PinIO
	button2Pin    gpio.PinIO
	button3Pin    gpio.PinIO
	button4Pin    gpio.PinIO
	buttonPage    int
	led1Pin       gpio.PinIO
	ledStateMutex sync.Mutex
)

func main() {
	// 首次載入配置
	envConfig = loadEnv()

	showLOGO = shouldShowLOGO()
	showDHT, DHTType, DHTPin = shouldShowDHT()
	onLoop = shouldOnLoop()
	// 每次循環延遲時間
	sleepTime = showSleepTime()
	originalSleep = sleepTime

	stepBy = defaultPage()
	// 最後一個顯示的步驟
	stepEnd = 6

	// 初始化 Periph.io 硬體層
	if _, err := host.Init(); err != nil {
		log.Fatal(err)
	}

	// 按鈕
	button1Pin = gpioreg.ByName(button1PinName())
	button2Pin = gpioreg.ByName(button2PinName())
	button3Pin = gpioreg.ByName(button3PinName())
	button4Pin = gpioreg.ByName(button4PinName())
	buttonPage = setButtonPage()
	led1Pin = gpioreg.ByName(led1PinName())

	// 初始化 GPIO 按鈕和 LED
	initGPIO()

	// 程式開始第一次執行
	firstRun = true

	// 啟動檔案監控 Goroutine
	go monitorEnvFile()

	// 為每個按鈕啟動一個 goroutine 來監聽按下事件
	go waitForButtonPress(button1Pin, "Button 1")
	go waitForButtonPress(button2Pin, "Button 2")
	go waitForButtonPress(button3Pin, "Button 3")
	go waitForButtonPress(button4Pin, "Button 4")

	// 初始化 I2C 匯流排
	bus, err := i2creg.Open("")
	if err != nil {
		log.Fatal(err)
	}
	defer bus.Close()

	// 初始化 SSD1306 顯示器 (地址 0x3C)
	opts := ssd1306.DefaultOpts
	opts.W = 128
	opts.H = 64
	dev, err := ssd1306.NewI2C(bus, &opts)
	if err != nil {
		log.Fatal(err)
	}
	defer dev.Halt()

	// 創建 image1bit.Image
	// bounds := dev.Bounds()
	// img := image1bit.NewVerticalLSB(bounds)
	img := image1bit.NewVerticalLSB(dev.Bounds())

	// 設置中斷訊號處理
	sigChan := make(chan os.Signal, 1)
	// signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	quitChan := make(chan bool)

	go func() {
		s := <-sigChan
		fmt.Println("\n接收到訊號:", s)
		fmt.Println("通知程式退出。")
		// 關閉 LED 燈
		if err := led1Pin.Out(gpio.Low); err != nil {
			log.Fatalf("Failed to set LED pin %s as output: %v", led1Pin, err)
		}
		// 釋放 GPIO 引腳
		if err := led1Pin.Halt(); err != nil {
			log.Fatalf("Failed to set LED pin %s as output: %v", led1Pin, err)
		}

		quitChan <- true
	}()

	// 主循環：讀取資訊並顯示
	for {
		select {
		// 優雅關閉程式
		case <-quitChan:
			fmt.Println("\n接收到中斷訊號，程式即將結束...")

			clearImage(img)
			drawText(img, 2, 0, testCenter("STOP", 18))
			drawText(img, 0, 3, "___________________")
			drawLargeText(img, 0, 7, "Bye", 3) // 縮放 3 倍
			// 更新顯示
			if err := dev.Draw(dev.Bounds(), img, image.Point{}); err != nil {
				log.Fatal(err)
			}
			time.Sleep(1 * time.Second)
			x := ""
			for i := range 3 {
				x = fmt.Sprintf("%v", "~")
				drawLargeText(img, 22+(i*7), 11, x, 3)

				// 更新顯示
				if err := dev.Draw(dev.Bounds(), img, image.Point{}); err != nil {
					log.Fatal(err)
				}
				time.Sleep(500 * time.Millisecond)
			}

			clearImage(img)
			dev.Draw(dev.Bounds(), img, image.Point{}) // 清空螢幕
			return
		default:
			clearImage(img)

			switch {
			case stepBy == 0 || firstRun:
				if showLOGO && firstRun {
					// 連續顯示所有幀
					showBMP(logoImage, dev, img, dev.Bounds(), 0)
					time.Sleep(time.Second * 2)
				}
				if stepBy == 0 {
					stepBy++
				}
				firstRun = false
				continue

			case stepBy == 1:
				if showDHT {
					err := dht.HostInit()
					if err != nil {
						fmt.Println("HostInit error:", err)
						return
					}

					// DHT22 數據 (GPIO4)
					dht, err := dht.NewDHT(DHTPin, dht.Fahrenheit, DHTType)
					if err != nil {
						fmt.Println("NewDHT error:", err)
						return
					}

					hum, temp, err := dht.ReadRetry(11)

					if err != nil {
						log.Printf("DHT22 讀取失敗: %v", err)
						lines := splitByN(err.Error(), 18)

						displayPagedError(dev, img, lines)
					} else {
						// 顯示 攝氏 溫度
						temp = (temp - 32) * 5.0 / 9.0

						drawText(img, 2, 0, testCenter("Temp / Hum", 18))
						drawText(img, 0, 3, "___________________")
						drawLargeText(img, 0, 4, fmt.Sprintf("%.0f", temp), 3)
						drawLargeText(img, 42, 16, "o", 1)
						drawLargeText(img, 25, 9, "C", 2)
						drawLargeText(img, 24, 6, fmt.Sprintf("%.0f", hum), 3)
						drawLargeText(img, 58, 14, "%", 2)
						drawText(img, 0, 50, "___________________")
					}
				} else {
					stepBy++
					continue
				}
			case stepBy == 2:
				// 顯示 HOSTNAME IP
				ipAddress, hostname := getIPAddress()

				drawText(img, 2, 0, testCenter(hostname, 18))
				drawText(img, 0, 3, "___________________")
				drawLargeText(img, 0, 6, ipAddress[:8], 2)
				drawLargeText(img, 15, 17, fmt.Sprintf("%7s", ipAddress[8:]), 2)
				drawText(img, 0, 50, "___________________")

			case stepBy == 3:
				// 顯示 CPU 使用率
				cpuUsage := getCPUUsage()

				drawText(img, 2, 0, testCenter("CPU Usage", 18))
				drawText(img, 0, 3, "___________________")
				drawLargeText(img, 2, 5, fmt.Sprintf("%5.1f", cpuUsage), 3)
				drawLargeText(img, 58, 14, "%", 2)
				drawText(img, 0, 50, "___________________")

			case stepBy == 4:
				// 顯示 CPU 溫度
				temperature := getCPUTemperature()

				drawText(img, 2, 0, testCenter("CPU Temperature", 18))
				drawText(img, 0, 3, "___________________")
				drawLargeText(img, 0, 5, fmt.Sprintf("%.2f", temperature), 3)
				drawText(img, 106, 18, "o")
				drawLargeText(img, 58, 10, "C", 2)
				drawText(img, 0, 50, "___________________")

			case stepBy == 5:
				// 顯示 RAM
				totalRAM, usedRAM, ramPct := getRAMUsage()

				drawText(img, 2, 0, testCenter("RAM Usage", 18))
				drawText(img, 0, 3, "___________________")

				// 使用文字顯示
				// drawLargeText(img, 6, 6, fmt.Sprintf("%6.2f", ramPct), 2)
				// drawText(img, 98, 25, "%")

				// 使用長條圖顯示
				drawBar(img, ramPct, 128, 14, 0, 22)

				drawLargeText(img, 0, 17, fmt.Sprintf("%5.2f", usedRAM), 2)
				drawText(img, 74, 44, "/")
				drawLargeText(img, 42, 17, fmt.Sprintf("%2.0f", totalRAM), 2)
				drawText(img, 114, 48, "GB")
				drawText(img, 0, 50, "___________________")

			case stepBy == 6:
				// 顯示 儲存 容量
				diskTotal, diskFree, diskUsed, diskPct := getDiskSpace()
				_, _, _, _ = diskTotal, diskFree, diskUsed, diskPct

				drawText(img, 2, 0, testCenter("Disk Used / Total", 18))
				drawText(img, 0, 3, "___________________")
				drawLargeText(img, 6, 6, fmt.Sprintf("%7.2f", diskUsed), 2)
				drawText(img, 112, 25, "GB")
				drawLargeText(img, 6, 17, fmt.Sprintf("%7.2f", diskTotal), 2)
				drawText(img, 112, 48, "GB")
				drawText(img, 0, 50, "___________________")
			}

			// 更新顯示
			if err := dev.Draw(dev.Bounds(), img, image.Point{}); err != nil {
				log.Fatal(err)
			}
			time.Sleep(sleepTime)

			// 切換顯示狀態頁面，onLoop 為 true 時，則循環顯示
			// 否則，顯示單頁面
			if onLoop {
				stepBy++
			}
			if stepBy > stepEnd {
				stepBy = 1
			}

		}
	}
}
