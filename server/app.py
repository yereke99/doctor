from flask import Flask, render_template, request, redirect, url_for

app = Flask(__name__)
app.config['SECRET_KEY'] = 'secret_key'

@app.route('/doctor', methods=['GET', 'POST'])
def doctor():
    if request.method == 'POST':
        # получаем данные из формы врача
        full_name   = request.form.get('full_name')
        doctor_type = request.form.get('doctor_type')
        experience  = request.form.get('experience')
        work_date   = request.form.get('work_date')
        start_time  = request.form.get('start_time')
        end_time    = request.form.get('end_time')
        document    = request.files.get('document')
        doc_filename = document.filename if document else 'Файл не загружен'

        # можно здесь сохранить данные в БД или сессии...

        # перенаправляем на страницу обновления доктора,
        # передав все параметры через query-параметры
        return redirect(url_for('update_doctor',
                                full_name=full_name,
                                specialty=doctor_type,
                                experience=experience,
                                work_date=work_date,
                                start_time=start_time,
                                end_time=end_time,
                                doc_filename=doc_filename))
    # GET — показываем исходную форму
    return render_template('doctor.html')


@app.route('/update-doctor', methods=['GET', 'POST'])
def update_doctor():
    if request.method == 'POST':
        # здесь обработка обновлённых данных (PUT/PATCH)
        # например, сохранение в базу
        # ...
        return '<h2>Данные успешно обновлены!</h2>'

    # при GET забираем переданные из /doctor параметры
    full_name   = request.args.get('full_name', '')
    specialty   = request.args.get('specialty', '')
    experience  = request.args.get('experience', '')
    work_date   = request.args.get('work_date', '')
    start_time  = request.args.get('start_time', '')
    end_time    = request.args.get('end_time', '')
    doc_filename= request.args.get('doc_filename', '')

    # рендерим update-doctor.html, прокидывая в него все переменные
    return render_template('update-doctor.html',
                           full_name=full_name,
                           specialty=specialty,
                           experience=experience,
                           work_date=work_date,
                           start_time=start_time,
                           end_time=end_time,
                           doc_filename=doc_filename)


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
    app.run(host='0.0.0.0', port=5001, debug=True)
