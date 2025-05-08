import numpy as np
import matplotlib.pyplot as plt

def plot_emergency_dynamometer_card():
    """
    Строит динамограмму аварийного состояния штангового насоса
    с характерными пиками и провалами нагрузки.
    """
    # Захардкоженные данные аварийного состояния
    position = np.array([0.00, 0.10, 0.20, 0.30, 0.40, 0.50, 0.40, 0.30, 0.20, 0.10, 0.00])
    load     = np.array([0.0,  6.0, 12.0, 18.0, 8.0,  4.0,  -5.0, -10.0, -3.0, -1.0, 0.0])

    plt.figure(figsize=(8, 6))
    plt.plot(position, load, '-o', linewidth=1.5)
    plt.xlabel('Перемещение штанги, м')
    plt.ylabel('Нагрузка на штангу, кН')
    plt.title('Аварийное состояние: динамограмма штангового насоса')
    plt.grid(True)
    plt.tight_layout()
    plt.show()

if __name__ == "__main__":
    plot_emergency_dynamometer_card()
