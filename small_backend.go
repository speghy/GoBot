package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	taskQueue   chan Task
	taskMap     sync.Map
	currentTask *Task      // Текущий выполняемый скрипт
	queueMutex  sync.Mutex // Мьютекс для синхронизации доступа к очереди
)

type Task struct {
	ID     string
	Script string
	Ctx    context.Context
	Cancel context.CancelFunc
	Status string           // Статус задачи: "queued", "running", "completed"
	Buffer *strings.Builder // Буфер для хранения вывода
}

// Убедимся, что папка logs существует
func ensureLogsDir() error {
	if _, err := os.Stat("logs"); os.IsNotExist(err) {
		log.Printf("Logs directory does not exist, creating...")
		err = os.Mkdir("logs", 0755)
		if err != nil {
			return fmt.Errorf("failed to create logs directory: %v", err)
		}
	}
	return nil
}

// Убедимся, что папка scripts существует
func ensureScriptsDir() error {
	if _, err := os.Stat("scripts"); os.IsNotExist(err) {
		log.Printf("Scripts directory does not exist, creating...")
		err = os.Mkdir("scripts", 0755)
		if err != nil {
			return fmt.Errorf("failed to create scripts directory: %v", err)
		}
	}
	return nil
}

// Очистка имени файла от небезопасных символов
func sanitizeFileName(filename string) string {
	// Удаляем небезопасные символы
	unsafeChars := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	for _, char := range unsafeChars {
		filename = strings.ReplaceAll(filename, char, "_")
	}
	return filename
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	// Парсим multipart-форму
	err := r.ParseMultipartForm(10 << 20) // 10 MB
	if err != nil {
		http.Error(w, "Failed to parse form", http.StatusBadRequest)
		return
	}

	// Извлекаем файл из формы
	file, handler, err := r.FormFile("file") // "file" — имя поля в форме
	if err != nil {
		http.Error(w, "Failed to get file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Убедимся, что папка scripts существует
	if err := ensureScriptsDir(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Очищаем имя файла от небезопасных символов
	safeFileName := sanitizeFileName(handler.Filename)
	scriptPath := filepath.Join("scripts", safeFileName)

	// Создаем файл для сохранения скрипта
	dst, err := os.Create(scriptPath)
	if err != nil {
		http.Error(w, "Failed to create script file", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Копируем содержимое файла из запроса в новый файл
	_, err = io.Copy(dst, file)
	if err != nil {
		http.Error(w, "Failed to save script", http.StatusInternalServerError)
		return
	}

	// Создаем контекст для задачи
	ctx, cancel := context.WithCancel(context.Background())
	task := Task{
		ID:     safeFileName, // Используем имя файла как ID
		Script: scriptPath,
		Ctx:    ctx,
		Cancel: cancel,
		Status: "queued",
		Buffer: &strings.Builder{}, // Инициализируем буфер
	}

	// Добавляем задачу в очередь
	taskQueue <- task
	taskMap.Store(task.ID, task)

	// Возвращаем имя файла
	fmt.Fprintf(w, "Task added. File name: %s", safeFileName)
}

func cancelHandler(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Path[len("/cancel/"):]

	// Получаем задачу из мапы
	task, ok := taskMap.Load(fileName)
	if !ok {
		http.Error(w, "Task not found", http.StatusNotFound)
		return
	}

	// Вызываем функцию отмены
	task.(Task).Cancel()
	fmt.Fprintf(w, "Task %s canceled", fileName)
}

func logHandler(w http.ResponseWriter, r *http.Request) {
	logName := r.URL.Path[len("/logs/"):]
	logPath := filepath.Join("logs", logName)

	// Проверяем, существует ли лог-файл
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		http.Error(w, "Log file not found", http.StatusNotFound)
		return
	}

	// Открываем лог-файл
	logFile, err := os.Open(logPath)
	if err != nil {
		http.Error(w, "Failed to open log file", http.StatusInternalServerError)
		return
	}
	defer logFile.Close()

	// Копируем содержимое лог-файла в ответ
	_, err = io.Copy(w, logFile)
	if err != nil {
		http.Error(w, "Failed to read log file", http.StatusInternalServerError)
		return
	}
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	// Получаем список файлов в папке logs
	files, err := os.ReadDir("logs")
	if err != nil {
		http.Error(w, "Failed to read logs directory", http.StatusInternalServerError)
		return
	}

	var logs []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".txt") {
			logs = append(logs, file.Name())
		}
	}

	// Возвращаем список логов в формате JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(logs)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	queueMutex.Lock()
	defer queueMutex.Unlock()

	if currentTask != nil {
		// Возвращаем текущее состояние буфера
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, currentTask.Buffer.String())
	} else {
		// Возвращаем None, если ничего не выполняется
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "None")
	}
}

func queueHandler(w http.ResponseWriter, r *http.Request) {
	var tasks []map[string]string

	// Проходим по всем задачам в taskMap
	taskMap.Range(func(key, value interface{}) bool {
		task := value.(Task)
		if task.Status == "queued" || task.Status == "running" {
			tasks = append(tasks, map[string]string{
				"id":     task.ID,
				"status": task.Status,
			})
		}
		return true
	})

	// Возвращаем JSON с незавершенными задачами
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

func currentHandler(w http.ResponseWriter, r *http.Request) {
	queueMutex.Lock()
	defer queueMutex.Unlock()

	if currentTask != nil {
		// Возвращаем имя текущего выполняемого скрипта
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, currentTask.ID)
	} else {
		// Возвращаем None, если ничего не выполняется
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "None")
	}
}

func executeScript(task Task) {
	// Убедимся, что папка logs существует
	if err := ensureLogsDir(); err != nil {
		log.Printf("Error: %v", err)
		return
	}

	// Обновляем статус задачи
	task.Status = "running"
	taskMap.Store(task.ID, task)

	// Устанавливаем текущую задачу
	queueMutex.Lock()
	currentTask = &task
	queueMutex.Unlock()

	// Запускаем Python-скрипт
	cmd := exec.CommandContext(task.Ctx, "python", task.Script)

	// Устанавливаем переменные среды PYTHONUNBUFFERED=1 и PYTHONIOENCODING=utf-8
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1", "PYTHONIOENCODING=utf-8")

	// Записываем вывод только в буфер
	cmd.Stdout = task.Buffer
	cmd.Stderr = task.Buffer

	err := cmd.Run()
	if err != nil {
		if task.Ctx.Err() == context.Canceled {
			log.Printf("Script %s was canceled", task.ID)
		} else {
			log.Printf("Script execution error: %v", err)
		}
	}

	// Обновляем статус задачи
	task.Status = "completed"
	taskMap.Store(task.ID, task)

	// Сбрасываем текущую задачу
	queueMutex.Lock()
	currentTask = nil
	queueMutex.Unlock()

	// Записываем буфер в файл .txt
	outputPath := filepath.Join("logs", task.ID+".txt")
	err = os.WriteFile(outputPath, []byte(task.Buffer.String()), 0644)
	if err != nil {
		log.Printf("Failed to write buffer to file: %v", err)
	}
}

func main() {
	taskQueue = make(chan Task, 100)

	// Запускаем исполнитель задач
	go func() {
		for task := range taskQueue {
			executeScript(task)
		}
	}()

	// Регистрируем HTTP-обработчики
	http.HandleFunc("/upload", uploadHandler)
	http.HandleFunc("/cancel/", cancelHandler)
	http.HandleFunc("/logs/", logHandler)
	http.HandleFunc("/logs", logsHandler)
	http.HandleFunc("/status", statusHandler)
	http.HandleFunc("/queue", queueHandler)
	http.HandleFunc("/current", currentHandler)

	// Обслуживаем статические файлы из папки static
	fs := http.FileServer(http.Dir("static"))
	http.Handle("/", fs)

	// Запускаем сервер
	fmt.Println("Server started at http://localhost:8080")
	http.ListenAndServe(":8080", nil)
}
