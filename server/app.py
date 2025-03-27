from flask import Flask, render_template, request

app = Flask(__name__)
app.config['SECRET_KEY'] = 'secret_key'

@app.route('/doctor', methods=['GET', 'POST'])
def doctor():
    if request.method == 'POST':
        # Получаем данные из формы врача
        full_name   = request.form.get('full_name')
        doctor_type = request.form.get('doctor_type')
        experience  = request.form.get('experience')
        work_date   = request.form.get('work_date')
        start_time  = request.form.get('start_time')
        end_time    = request.form.get('end_time')
        document    = request.files.get('document')
        doc_filename = document.filename if document else 'Файл не загружен'
        # Для демонстрации возвращаем полученные данные
        return f"""
        <h2>Данные врача получены:</h2>
        <p>ФИО: {full_name}</p>
        <p>Специализация: {doctor_type}</p>
        <p>Стаж: {experience} лет</p>
        <p>Рабочая дата: {work_date}</p>
        <p>Время: {start_time} - {end_time}</p>
        <p>Документ: {doc_filename}</p>
        """
    return render_template('doctor.html')

@app.route('/client', methods=['GET', 'POST'])
def client():
    if request.method == 'POST':
        # Получаем данные из формы клиента
        full_name = request.form.get('full_name')
        gender    = request.form.get('gender')
        age       = request.form.get('age')
        problem   = request.form.get('problem')
        contacts  = request.form.get('contacts')
        address   = request.form.get('address')
        # Для демонстрации возвращаем полученные данные
        return f"""
        <h2>Данные клиента получены:</h2>
        <p>ФИО: {full_name}</p>
        <p>Пол: {gender}</p>
        <p>Возраст: {age}</p>
        <p>Проблема: {problem}</p>
        <p>Контакты: {contacts}</p>
        <p>Адрес: {address}</p>
        """
    return render_template('client.html')

if __name__ == '__main__':
    # Запуск сервера на всех интерфейсах и порту 5001
    app.run(host='0.0.0.0', port=5001, debug=True)
