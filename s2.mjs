import axios from "axios";
import http from "http";
import https from "https";
import { appendFile } from "fs/promises";

/** ==== CONFIG ==== */
const IAM_TOKEN = process.argv[2];
if (!IAM_TOKEN) {
  console.error(
    "Usage: node s2.mjs <IAM_TOKEN>\n" +
      "IAM_TOKEN — токен сервисного пользователя (X-Auth-Token)."
  );
  process.exit(1);
}

const PROJECT_ID = "a907e0f1b8a14d639ab5961884e49752";  //МЕНЯТЬ
// Можно добавить несколько регионов, скрипт будет выбирать случайно
const REGIONS = ["ru-2", "ru-3"]; 

const TARGET_CIDRS = [
  "5.101.50.0/23",
"5.178.85.0/24",
"5.188.56.0/24",
"5.188.112.0/22",
"5.188.118.0/23",
"5.188.158.0/23",
"5.189.239.0/24",
"31.41.157.0/24",
"31.172.128.0/24",
"31.184.211.0/24",
"31.184.215.0/24",
"31.184.218.0/24",
"31.184.253.0/24",
"31.184.254.0/24",
"37.9.4.0/24",
"37.9.13.0/24",
"78.24.181.0/24",
"80.93.187.0/24",
"80.249.145.0/24",
"80.249.146.0/23",
"81.163.22.0/23",
"82.202.192.0/19",
"82.202.224.0/22",
"82.202.228.0/24",
"82.202.230.0/23",
"82.202.233.0/24",
"82.202.234.0/23",
"82.202.236.0/22",
"82.202.240.0/20",
"84.38.181.0/24",
"84.38.182.0/24",
"84.38.185.0/24",
"87.228.101.0/24",
"178.72.0.0/22",
"185.91.53.0/24",
"185.91.54.0/24",
"188.68.218.0/24"
];

const MAX_ATTEMPTS = 50000; 
const RETRY_DELAY_MS = 1900;    // Минимальный интервал между попытками (1 сек)
const CONSECUTIVE_ATTEMPTS = 10; // Каждые 10 попыток
const TIME_DELAY = 330000;      // Cooldown 5 минут (300000 мс)

let dynamicDelay = RETRY_DELAY_MS;

// Файл для сохранения всех IP с регионом
const LOG_FILE = "found_ips.txt";

/** ==== HELPERS ==== */
function ipToInt(ip) {
  return ip.split(".").reduce((acc, oct) => ((acc << 8) + (oct | 0)) >>> 0, 0) >>> 0;
}

function parseCidr(cidr) {
  const [base, maskStr] = cidr.split("/");
  const mask = parseInt(maskStr, 10);
  const baseInt = ipToInt(base);
  const maskInt = mask === 0 ? 0 : (~0 << (32 - mask)) >>> 0;
  return { baseInt: baseInt & maskInt, maskInt };
}

function isIpInCidr(ip, cidr) {
  const ipInt = ipToInt(ip);
  const { baseInt, maskInt } = parseCidr(cidr);
  return (ipInt & maskInt) === baseInt;
}

function checkIpInAnyCidr(ip, cidrs) {
  for (const cidr of cidrs) {
    if (isIpInCidr(ip, cidr)) return cidr;
  }
  return null;
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

function pickRandom(arr) {
  return arr[Math.floor(Math.random() * arr.length)];
}

/** ==== AXIOS ==== */
const api = axios.create({
  baseURL: "https://api.selectel.ru/vpc/resell/v2",
  headers: {
    "X-Auth-Token": IAM_TOKEN,
    "Content-Type": "application/json",
  },
  timeout: 30000,
  httpAgent: new http.Agent({ keepAlive: true }),
  httpsAgent: new https.Agent({ keepAlive: true }),
});

/** ==== CORE FUNCTIONS ==== */

async function createFloatingIp(region) {
  const body = { floatingips: [{ quantity: 1, region }] };
  const { data } = await api.post(`/floatingips/projects/${PROJECT_ID}`, body);
  return data.floatingips?.[0];
}

async function deleteFloatingIp(floatingipId) {
  try {
    await api.delete(`/floatingips/${encodeURIComponent(floatingipId)}`);
    return true;
  } catch (e) {
    if (e.response && e.response.status === 404) return true; // Уже удален
    if (e.response) {
      console.error(`Ошибка удаления ${floatingipId}:`, e.response.status);
    } else {
      console.error(`Ошибка удаления ${floatingipId}:`, e.message);
    }
    return false;
  }
}

async function run() {
  console.log(`Целевые подсети: ${TARGET_CIDRS.join(", ")}`);
  console.log(`Регионы: ${REGIONS.join(", ")}`);
  console.log(`Логика: ${CONSECUTIVE_ATTEMPTS} попыток быстро -> пауза ${TIME_DELAY/1000} сек.`);
  console.log(`Максимум попыток: ${MAX_ATTEMPTS}\n`);
  
  const foundAddresses = {};
  TARGET_CIDRS.forEach((cidr) => { foundAddresses[cidr] = []; });

  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
    
    // Логика паузы каждые 10 попыток
    if (attempt % CONSECUTIVE_ATTEMPTS === 0) {
      console.log(`\n⏸ Достигнуто ${CONSECUTIVE_ATTEMPTS} попыток — COOLDOWN 5 минут...\n`);
      await sleep(TIME_DELAY);
      console.log(`⏳ Пауза завершена. Продолжаем.\n`);
    }

    const region = pickRandom(REGIONS);
    console.log(`[Попытка ${attempt}/${MAX_ATTEMPTS}] Регион: ${region}...`);

    try {
      const floatingip = await createFloatingIp(region);
      const floatingipId = floatingip?.id;
      const ip = floatingip?.floating_ip_address;

      if (!floatingipId || !ip) {
        console.error("Не удалось извлечь IP из ответа.");
        await sleep(RETRY_DELAY_MS);
        continue;
      }

      const now = new Date().toLocaleTimeString('ru-RU');
      console.log(`[${now}] Получен IP: ${ip}`);

      // Проверка на попадание в целевые подсети
      const matchedCidr = checkIpInAnyCidr(ip, TARGET_CIDRS);
      const isFound = matchedCidr !== null;

      // Запись в файл: IP - Регион (и метка НАЙДЕН если совпало)
      let logEntry = "";
      if (isFound) {
        logEntry = `${ip} - НАЙДЕН (${matchedCidr}) [Регион: ${region}]\n`;
      } else {
        logEntry = `${ip} [Регион: ${region}]\n`;
      }

      try {
        await appendFile(LOG_FILE, logEntry);
      } catch (err) {
        console.error(`️ Ошибка записи в ${LOG_FILE}:`, err.message);
      }

      if (isFound) {
        console.log(`✅ IP попал в подсеть ${matchedCidr}! Сохранено.`);
        foundAddresses[matchedCidr].push({ floatingipId, ip, region, attempt });

        let totalFound = 0;
        TARGET_CIDRS.forEach((cidr) => {
          const count = foundAddresses[cidr].length;
          totalFound += count;
        });
        console.log(` Всего найдено: ${totalFound}\n`);

        // Найденный IP не удаляем сразу, пусть висит (или можно удалить, если не нужен)
        // В данном коде оставляем его, как в предыдущих версиях для найденных
        await sleep(RETRY_DELAY_MS);
        continue;
      }

      // Если не найден - удаляем
      console.log("❌ IP вне целевых подсетей. Удаляю...");
      await deleteFloatingIp(floatingipId);
      
      // Минимальная задержка перед следующей попыткой
      await sleep(RETRY_DELAY_MS);

    } catch (error) {
      if (error.response) {
        const { status } = error.response;
        console.error("Ошибка API:", status);

        if (status === 429) { 

          console.log("Лимит запросов (429).");
        } else {
          dynamicDelay = Math.max(RETRY_DELAY_MS, dynamicDelay - 100);
        }
        await sleep(dynamicDelay);
      } else if (error.request) {
        console.error("Нет ответа от API.");
        await sleep(RETRY_DELAY_MS);
      } else {
        console.error("Ошибка:", error.message);
        await sleep(RETRY_DELAY_MS);
      }
    }
  }

  // Итоговая статистика
  console.log("\n" + "=".repeat(70));
  console.log(` Завершено ${MAX_ATTEMPTS} попыток`);
  console.log("=".repeat(70));
  
  let totalFound = 0;
  TARGET_CIDRS.forEach((cidr) => {
    const count = foundAddresses[cidr].length;
    totalFound += count;
    if (count > 0) {
      console.log(`\n📊 Подсеть ${cidr}: найдено ${count} адресов`);
      foundAddresses[cidr].forEach((addr, idx) => {
        console.log(`   ${idx + 1}. ${addr.ip} (Регион: ${addr.region})`);
      });
    }
  });

  console.log("\n" + "=".repeat(70));
  console.log(`✅ ИТОГО найдено адресов: ${totalFound}`);
  console.log("=".repeat(70));
  console.log(`💾 Полный лог с регионами сохранен в: ${LOG_FILE}`);
}

run().catch((e) => {
  console.error("Фатальная ошибка:", e);
});