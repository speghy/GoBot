import requests
import time

def send_script_to_backend(file_path, backend_url="http://localhost:8080/upload"):
    """
    Отправляет Python-скрипт на бэкенд и получает ответ.

    :param file_path: Путь к файлу с Python-скриптом.
    :param backend_url: URL бэкенда для загрузки скрипта.
    :return: Имя файла (ID задачи), если скрипт успешно отправлен.
    """
    try:
        # Открываем файл и отправляем его на бэкенд
        with open(file_path, "rb") as file:
            files = {"file": file}
            response = requests.post(backend_url, files=files)

        # Проверяем статус ответа
        if response.status_code == 200:
            print("Скрипт успешно отправлен! Ответ сервера:")
            print(response.text)
            # Возвращаем имя файла (ID задачи)
            return response.text.strip().split()[-1]  # Извлекаем имя файла из ответа
        else:
            print(f"Ошибка: {response.status_code}")
            print(response.text)
            return None

    except Exception as e:
        print(f"Произошла ошибка: {e}")
        return None

def get_log_status(task_id, backend_url="http://localhost:8080/status"):
    """
    Запрашивает текущий статус выполнения скрипта.

    :param task_id: Имя файла (ID задачи).
    :param backend_url: URL бэкенда для получения статуса.
    """
    try:
        response = requests.get(backend_url)
        if response.status_code == 200:
            print("Текущий статус выполнения:")
            print(response.text)
        else:
            print(f"Ошибка при запросе статуса: {response.status_code}")
    except Exception as e:
        print(f"Произошла ошибка: {e}")

def monitor_log_status(task_id, interval=0.5):
    """
    Периодически запрашивает статус выполнения скрипта.

    :param task_id: Имя файла (ID задачи).
    :param interval: Интервал между запросами (в секундах).
    """
    try:
        while True:
            get_log_status(task_id)
            time.sleep(interval)
    except KeyboardInterrupt:
        print("Мониторинг завершен.")

if __name__ == "__main__":
    # Укажи путь к файлу с Python-скриптом
    script_path = "C:/GoBot/print_script/print.py"

    # Отправляем скрипт на бэкенд
    task_id = send_script_to_backend(script_path)

    # Если скрипт успешно отправлен, начинаем мониторинг
    if task_id:
        print(f"Начинаем мониторинг выполнения скрипта {task_id}...")
        monitor_log_status(task_id)