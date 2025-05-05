// 關於樹莓派的顯示和控制
package main

import (
	"bufio"
	"image"
	"log"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/joho/godotenv"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
	"periph.io/x/conn/v3/gpio"
	"periph.io/x/conn/v3/gpio/gpioreg"
	"periph.io/x/devices/v3/ssd1306"
	"periph.io/x/devices/v3/ssd1306/image1bit"
)

func showBMP(imageData [][]byte, dev *ssd1306.Dev, img *image1bit.VerticalLSB, bounds image.Rectangle, sleepTime time.Duration) {
	for _, frameData := range imageData {
		if len(frameData) <= len(img.Pix) {
			copy(img.Pix[:len(frameData)], frameData)
			if err := dev.Draw(bounds, img, image.Point{}); err != nil {
				log.Fatal(err)
			}
			// 這裡可以添加一個小的延遲，以控制動畫的速度
			time.Sleep(time.Millisecond * 100) // 例如，每幀延遲 100 毫秒
		} else {
			log.Printf("幀資料長度 (%d) 大於螢幕緩衝區長度 (%d)，可能會截斷", len(frameData), len(img.Pix))
			copy(img.Pix, frameData)
			if err := dev.Draw(bounds, img, image.Point{}); err != nil {
				log.Fatal(err)
			}
			_ = sleepTime
			// time.Sleep(time.Millisecond * 100)
		}
		// 如果您希望所有幀快速連續顯示而不停頓，則不需要 time.Sleep()
	}
}

// 獲取 IP 位址
func getIPAddress() (string, string) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "Unknown"
	}

	interfaces, err := net.Interfaces()
	if err != nil {
		return "N/A", hostname
	}
	for _, i := range interfaces {
		if i.Name == "wlan0" || i.Name == "eth0" { // 根據您的網路介面名稱修改
			addrs, err := i.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				if ip.To4() != nil && !ip.IsLoopback() {
					return ip.String(), hostname
				}
			}
		}
	}
	return "N/A", hostname
}

// 獲取 CPU 使用率
func getCPUUsage() float64 {
	var idle0, total0, idle1, total1 int64
	procStat, err := os.Open("/proc/stat")
	if err != nil {
		return 0.0
	}
	defer procStat.Close()

	reader := bufio.NewReader(procStat)
	line, err := reader.ReadString('\n')
	if err != nil {
		return 0.0
	}
	parts := strings.Fields(line)
	if len(parts) >= 5 && parts[0] == "cpu" {
		idle0, _ = strconv.ParseInt(parts[4], 10, 64)
		for i := 1; i < len(parts); i++ {
			if i > 0 { // 確保索引有效
				temp, _ := strconv.ParseInt(parts[i], 10, 64)
				total0 += temp
			}
		}
	}
	runtime.Gosched()
	time.Sleep(100 * time.Millisecond) // 短暫延遲以獲取第二個快照

	procStat, err = os.Open("/proc/stat")
	if err != nil {
		return 0.0
	}
	defer procStat.Close()
	reader = bufio.NewReader(procStat)
	line, err = reader.ReadString('\n')
	if err != nil {
		return 0.0
	}
	parts = strings.Fields(line)
	if len(parts) >= 5 && parts[0] == "cpu" {
		idle1, _ = strconv.ParseInt(parts[4], 10, 64)
		for i := 1; i < len(parts); i++ {
			if i > 0 { // 確保索引有效
				temp, _ := strconv.ParseInt(parts[i], 10, 64)
				total1 += temp
			}
		}
	}
	if total1-total0 == 0 {
		return 0.0
	}
	idleDelta := idle1 - idle0
	totalDelta := total1 - total0
	cpuUsage := 100.0 * float64(totalDelta-idleDelta) / float64(totalDelta)
	return cpuUsage
}

// 獲取 RAM 使用率
func getRAMUsage() (float64, float64, float64) {
	memInfo, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0.0, 0.0, 0.0
	}
	defer memInfo.Close()

	reader := bufio.NewReader(memInfo)
	var totalRAM float64
	var availableRAM float64

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		fields := strings.Split(line, ":")
		if len(fields) == 2 {
			key := strings.TrimSpace(fields[0])
			valueStr := strings.TrimSpace(strings.ReplaceAll(fields[1], " kB", ""))
			value, err := strconv.ParseFloat(valueStr, 64)
			if err != nil {
				continue
			}
			switch key {
			case "MemTotal":
				totalRAM = value / 1024 / 1024 // GB
			case "MemAvailable":
				availableRAM = value / 1024 / 1024 // GB
			}
		}
	}
	usedRAM := totalRAM - availableRAM
	usagePct := float64(usedRAM) / float64(totalRAM) * 100
	return totalRAM, usedRAM, usagePct
}

// 獲取 CPU 溫度
func getCPUTemperature() float64 {
	content, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0.0
	}
	tempStr := strings.TrimSpace(string(content))
	tempInt, err := strconv.Atoi(tempStr)
	if err != nil {
		return 0.0
	}
	return float64(tempInt) / 1000.0
}

func getDiskSpace() (float64, float64, float64, float64) {
	fs := syscall.Statfs_t{}
	err := syscall.Statfs("/", &fs)
	if err != nil {
		return 0, 0, 0, 0
	}
	total := float64(fs.Blocks*uint64(fs.Bsize)) / 1024 / 1024 / 1024
	free := float64(fs.Bfree*uint64(fs.Bsize)) / 1024 / 1024 / 1024
	used := total - free
	usagePct := used / total * 100
	return total, free, used, usagePct
}

// 清空畫面
func clearImage(img *image1bit.VerticalLSB) {
	for y := range img.Bounds().Dy() {
		for x := range img.Bounds().Dx() {
			img.Set(x, y, image1bit.Off)
		}
	}
}

// 繪製文字
func drawText(img *image1bit.VerticalLSB, x, y int, text string) {
	// 創建一個新的圖像作為文字源
	textImg := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	d := &font.Drawer{
		Dst:  textImg,
		Src:  image.White,
		Face: basicfont.Face7x13,
		Dot:  fixed.P(x, y+13), // +13 因為字體高度是13
	}
	d.DrawString(text)

	// 將文字圖像轉換到我們的1bit圖像
	for ty := range textImg.Bounds().Dy() {
		for tx := range textImg.Bounds().Dx() {
			r, _, _, _ := textImg.At(tx, ty).RGBA()
			if r > 0xFF00 { // 如果像素是亮的
				img.Set(tx, ty, image1bit.On)
			}
		}
	}
}

// 繪製放大的文字 (簡化方法)
func drawLargeText(img *image1bit.VerticalLSB, x, y int, text string, scale int) {
	for _, r := range text {
		charImg := image1bit.NewVerticalLSB(image.Rect(0, 0, 7, 13)) // basicfont 的尺寸
		d := font.Drawer{
			Dst:  charImg,
			Src:  image.White,
			Face: basicfont.Face7x13,
			Dot:  fixed.P(0, 13),
		}
		d.DrawString(string(r))

		// 將字元圖像放大並繪製到主圖像
		for cy := range 13 {
			for cx := range 7 {
				if charImg.BitAt(cx, cy) == image1bit.On {
					for ly := range scale {
						for lx := range scale {
							drawX := x*scale + cx*scale + lx
							drawY := y*scale + cy*scale + ly
							if drawX < img.Bounds().Dx() && drawY < img.Bounds().Dy() {
								img.Set(drawX, drawY, image1bit.On)
							}
						}
					}
				}
			}
		}
		x += 7 // basicfont 的寬度
	}
}

func displayPagedError(dev *ssd1306.Dev, img *image1bit.VerticalLSB, lines []string) {
	const linesPerPage = 3
	const lineHeight = 16

	for i := 0; i < len(lines); i += linesPerPage {
		clearImage(img)
		// end := i + linesPerPage
		// if end > len(lines) {
		// 	end = len(lines)
		// }
		end := min(i+linesPerPage, len(lines))

		drawText(img, 2, 0, testCenter("Error", 18))
		drawText(img, 0, 3, "___________________")

		for j, line := range lines[i:end] {
			drawText(img, 0, lineHeight*(j+1), line)
		}
		// 更新顯示
		if err := dev.Draw(dev.Bounds(), img, image.Point{}); err != nil {
			log.Fatal(err)
		}

		if end < len(lines) {
			time.Sleep(5 * time.Second)
		}
	}
}

func loadEnv() map[string]string {
	// 假設 .env 檔案與可執行檔案在同一目錄
	envPath := filepath.Join(".", ".env")
	envMap, err := godotenv.Read(envPath)
	if err != nil {
		log.Println("Error loading .env file:", err)
		return make(map[string]string)
	}
	return envMap
}

func shouldShowDHT() (bool, string, string) {
	configMutex.RLock()
	defer configMutex.RUnlock()
	showDHTStr := envConfig["SHOW_DHT"]
	DHTTypeStr := envConfig["DHT_TYPE"]
	if DHTTypeStr == "" {
		DHTTypeStr = "DHT11" // 預設值
	}
	DHTPinStr := envConfig["DHT_PIN"]
	if DHTPinStr == "" {
		DHTPinStr = "4" // 預設值
	}
	return strings.ToLower(showDHTStr) == "true", DHTTypeStr, DHTPinStr
}

func shouldShowLOGO() bool {
	configMutex.RLock()
	defer configMutex.RUnlock()
	showLogoStr := envConfig["SHOW_LOGO"]
	return strings.ToLower(showLogoStr) == "true"
}

func shouldOnLoop() bool {
	configMutex.RLock()
	defer configMutex.RUnlock()
	onLoopStr := envConfig["ON_LOOP"]
	return strings.ToLower(onLoopStr) == "true"
}

func defaultPage() int {
	configMutex.RLock()
	defer configMutex.RUnlock()
	defaultPageStr := envConfig["DEFAULT_PAGE"]
	if defaultPageStr == "" {
		defaultPageStr = "0" // 預設值
	}
	stepBy, err := strconv.Atoi(defaultPageStr)
	if err != nil {
		log.Println("Error converting DEFAULT_PAGE to int:", err)
		return 0 // 預設值
	}
	if stepBy <= 0 {
		return 0
	}
	return stepBy
}

func showSleepTime() time.Duration {
	configMutex.RLock()
	defer configMutex.RUnlock()
	showSleep := envConfig["SLEEP_TIME"]
	if showSleep == "" {
		showSleep = "3" // 預設值
	}
	timeSleep, err := strconv.Atoi(showSleep)
	if err != nil {
		log.Println("Error converting SLEEP_TIME to int:", err)
		return 3 * time.Second // 預設值
	}

	return time.Duration(timeSleep) * time.Second
}

// 取 .env 檔案中的 GPIO_BUTTON1 設定
func button1PinName() string {
	configMutex.RLock()
	defer configMutex.RUnlock()
	pinName := envConfig["GPIO_BUTTON1"]
	if pinName == "" {
		pinName = "GPIO17" // 預設值
	}
	return pinName
}

// 取 .env 檔案中的 GPIO_BUTTON2 設定
func button2PinName() string {
	configMutex.RLock()
	defer configMutex.RUnlock()
	pinName := envConfig["GPIO_BUTTON2"]
	if pinName == "" {
		pinName = "GPIO27" // 預設值
	}
	return pinName
}

// 取 .env 檔案中的 GPIO_BUTTON3 設定
func button3PinName() string {
	configMutex.RLock()
	defer configMutex.RUnlock()
	pinName := envConfig["GPIO_BUTTON3"]
	if pinName == "" {
		pinName = "GPIO22" // 預設值
	}
	return pinName
}

// 取 .env 檔案中的 GPIO_BUTTON4 設定
func button4PinName() string {
	configMutex.RLock()
	defer configMutex.RUnlock()
	pinName := envConfig["GPIO_BUTTON4"]
	if pinName == "" {
		pinName = "GPIO23" // 預設值
	}
	return pinName
}

// 取 .env 檔案中的 BUTTON_PAGE 設定
func setButtonPage() int {
	pageStr := envConfig["BUTTON_PAGE"]
	pageStrInt, err := strconv.Atoi(pageStr)
	if err != nil {
		log.Println("Error converting DEFAULT_PAGE to int:", err)
		return 4 // 預設值
	}
	if pageStrInt > stepEnd {
		return stepEnd // 預設值
	}
	if pageStrInt <= 0 {
		return 1
	}
	return pageStrInt
}

// 取 .env 檔案中的 GPIO_LED1 設定
func led1PinName() string {
	configMutex.RLock()
	defer configMutex.RUnlock()
	pinName := envConfig["GPIO_LED1"]
	if pinName == "" {
		pinName = "GPIO26" // 預設值
	}
	return pinName
}

func monitorEnvFile() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	var lastWriteTime time.Time
	var lastWriteFile string

	err = watcher.Add(".env")
	if err != nil {
		log.Fatal(err)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Write == fsnotify.Write {
				now := time.Now()
				if event.Name == lastWriteFile && now.Sub(lastWriteTime) < 1000*time.Millisecond {
					// log.Println("Ignored duplicate write event:", event)
					continue
				}

				// log.Println("Write event:", event)
				log.Println(".env file modified, reloading configuration.")

				// 增加延遲以確保檔案完全寫入
				time.Sleep(500 * time.Millisecond)

				configMutex.Lock()
				envConfig = loadEnv()
				configMutex.Unlock()

				showLOGO = shouldShowLOGO()
				showDHT, DHTType, DHTPin = shouldShowDHT()
				onLoop = shouldOnLoop()
				stepBy = defaultPage()
				// 每次循環延遲時間
				sleepTime = showSleepTime()
				originalSleep = sleepTime

				// GPIO 按鈕、LED
				button1Pin = gpioreg.ByName(button1PinName())
				button2Pin = gpioreg.ByName(button2PinName())
				button3Pin = gpioreg.ByName(button3PinName())
				button4Pin = gpioreg.ByName(button4PinName())
				buttonPage = setButtonPage()
				led1Pin = gpioreg.ByName(led1PinName())
				// 初始化 GPIO 按鈕和 LED
				initGPIO()

				lastWriteTime = now
				lastWriteFile = event.Name
				printEnvConfig(envConfig)
			} else {
				// log.Println("Other event:", event) // 可選：記錄其他事件
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Println("error:", err)
		}
	}
}

func printEnvConfig(config map[string]string) {
	for key, value := range config {
		log.Printf("%s:%s\n", key, value)
	}
}

// 在 image1bit.Image 上繪製長條圖
func drawBar(img *image1bit.VerticalLSB, percentage float64, barWidth, barHeight, barX, barY int) {

	filledWidth := int(math.Round(percentage / 100 * float64(barWidth)))

	// 繪製長條圖的邊框 (空心矩形)
	borderColor := image.White
	img.Set(barX, barY, borderColor)
	img.Set(barX+barWidth-1, barY, borderColor)
	img.Set(barX, barY+barHeight-1, borderColor)
	img.Set(barX+barWidth-1, barY+barHeight-1, borderColor)
	for x := barX + 1; x < barX+barWidth-1; x++ {
		img.Set(x, barY, borderColor)
		img.Set(x, barY+barHeight-1, borderColor)
	}
	for y := barY + 1; y < barY+barHeight-1; y++ {
		img.Set(barX, y, borderColor)
		img.Set(barX+barWidth-1, y, borderColor)
	}

	// 填充長條圖
	fillColor := image.White
	for x := barX; x < barX+filledWidth; x++ {
		for y := barY; y < barY+barHeight; y++ {
			img.Set(x, y, fillColor)
		}
	}
}

// 初始化 GPIO 按鈕和 LED
func initGPIO() {
	// 初始化 GPIO
	// 這裡可以添加初始化 GPIO 的代碼
	// 例如，設置引腳模式、配置中斷等
	// 將 LED 引腳設置為輸出
	ledStateMutex.Lock()
	if onLoop {
		if err := led1Pin.Out(gpio.High); err != nil {
			log.Fatalf("Failed to set LED pin %s as output: %v", led1Pin, err)
		}
	} else {
		if err := led1Pin.Out(gpio.Low); err != nil {
			log.Fatalf("Failed to set LED pin %s as output: %v", led1Pin, err)
		}
	}
	ledStateMutex.Unlock()
	log.Printf("LED control on pin %s\n", led1Pin)

	// 將按鈕引腳設置為輸入，並配置上拉 (如果您的硬體需要)
	pull := gpio.PullUp
	edge := gpio.FallingEdge // 假設按下是下降沿

	if err := button1Pin.In(pull, edge); err != nil {
		log.Fatalf("Failed to set button 1 pin %s as input with pull-up and falling edge detection: %v", button1Pin, err)
	}
	log.Printf("Button 1 input on pin %s\n", button1Pin)

	if err := button2Pin.In(gpio.PullUp, gpio.FallingEdge); err != nil {
		log.Fatalf("Failed to set button 2 pin %s as input with pull-up and falling edge detection: %v", button2Pin, err)
	}
	log.Printf("Button 2 input on pin %s\n", button2Pin)

	if err := button3Pin.In(gpio.PullUp, gpio.FallingEdge); err != nil {
		log.Fatalf("Failed to set button 3 pin %s as input with pull-up and falling edge detection: %v", button3Pin, err)
	}
	log.Printf("Button 3 input on pin %s\n", button3Pin)

	if err := button4Pin.In(gpio.PullUp, gpio.FallingEdge); err != nil {
		log.Fatalf("Failed to set button 4 pin %s as input with pull-up and falling edge detection: %v", button4Pin, err)
	}
	log.Printf("Button 4 input on pin %s\n", button4Pin)

}

// 等待按鈕按下事件
func waitForButtonPress(pin gpio.PinIO, buttonName string) {
	for {
		pin.WaitForEdge(-1 * time.Second) // 等待邊緣觸發 (按下或釋放)
		time.Sleep(50 * time.Millisecond) // 簡單的防彈跳延遲
		if !pin.Read() {                  // 檢查是否為按下狀態 (假設按下為 Low)
			log.Printf("%s 按下，", buttonName)
			// 在這裡直接處理按鈕按下的事件
			switch buttonName {
			case "Button 1":
				// 處理 Button 1 的事件
				if stepBy <= 1 {
					stepBy = stepEnd
				} else {
					stepBy--
				}
				log.Println("上一頁：", stepBy)
				stopLoop()

			case "Button 2":
				// 處理 Button 2 的事件
				if stepBy >= stepEnd {
					stepBy = 1
				} else {
					stepBy++
				}
				log.Println("下一頁：", stepBy)
				stopLoop()

			case "Button 3":
				// 處理 Button 3 的事件
				stepBy = buttonPage
				log.Println("跳到：", buttonPage, " 頁")
				stopLoop()

			case "Button 4":
				// 處理 Button 4 的事件
				onLoop = !onLoop
				if onLoop {
					sleepTime = originalSleep
					ledStateMutex.Lock()
					if err := led1Pin.Out(gpio.High); err != nil {
						log.Fatalf("Failed to set LED pin %s as output: %v", led1Pin, err)
					}
					ledStateMutex.Unlock()
					log.Printf("LED pin %s 點亮 循環：%v\n", led1Pin, onLoop)
				} else {
					stopLoop()
				}

			}
			time.Sleep(200 * time.Millisecond) // 避免快速重複觸發
		}
	}
}

// 停止循環顯示
func stopLoop() {
	ledStateMutex.Lock()
	sleepTime = 1 * time.Second
	if err := led1Pin.Out(gpio.Low); err != nil {
		log.Fatalf("Failed to set LED pin %s as output: %v", led1Pin, err)
	}
	ledStateMutex.Unlock()
	onLoop = false
	log.Printf("LED pin %s 熄滅 循環：%v\n", led1Pin, onLoop)
}
