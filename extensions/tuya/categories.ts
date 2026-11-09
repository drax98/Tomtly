// Every Tuya product category we know of, and the kind of card it gets.
//
// Sources, in order: Tuya's "List of category code" (103 categories,
// https://developer.tuya.com/en/docs/iot/standarddescription?id=K9i5ql6waswzq), then the documented and undocumented entries
// of DeviceCategory in Home Assistant's tuya integration (homeassistant/components/tuya/const.py), marked "HA", then the
// sidebar of Tuya's lighting docs, marked "docs". "qt" is Tuya's early, possibly unsupported category and stays "other".
//
// Kinds beyond docs/devices.md: switch, lock, appliance. kg under "Plugs" and a washing machine under "Other" would mislead.
export const CATEGORIES: Record<string, string> = {
  // Lighting
  dj: "light", // Light
  xdd: "light", // Ceiling light
  fwd: "light", // Ambiance light
  dc: "light", // String lights
  dd: "light", // Strip lights
  gyd: "light", // Motion sensor light
  tyndj: "light", // Solar light
  tgq: "light", // Dimmer
  tgkg: "light", // Dimmer switch
  sxd: "light", // Spotlight, docs
  dsd: "light", // Filament light, HA
  hcdd: "light", // Chasing tape light, HA
  hxd: "light", // Wake up light, HA
  mbd: "light", // Unknown light product, HA
  qjdcz: "light", // Unknown product with light capabilities, HA
  tyd: "light", // Outdoor flood light, HA
  // Switches, sockets, power
  kg: "switch", // Switch
  cjkg: "switch", // Scene switch
  ckqdkg: "switch", // Card switch
  szjqr: "switch", // Fingerbot, HA
  znjxs: "switch", // Fingerbot, HA
  cz: "plug", // Socket
  aqcz: "plug", // Single phase power meter with a switch, HA
  pc: "infrastructure", // Power strip; this home keeps it here, see TUNED_CATEGORIES in rules.ts
  tdq: "infrastructure", // Dimmer per HA; this home keeps it here, see TUNED_CATEGORIES in rules.ts
  dlq: "infrastructure", // Circuit breaker
  znjdq: "infrastructure", // Circuit breaker, HA
  zndb: "infrastructure", // Smart electricity meter
  qccdz: "infrastructure", // AC charging, HA
  xnyjcn: "infrastructure", // Micro storage inverter, HA
  znnbq: "infrastructure", // Micro inverter, HA
  // Gateways and remotes
  wg2: "infrastructure", // Gateway control, HA
  ykq: "infrastructure", // Remote control
  wnykq: "infrastructure", // Smart Wi-Fi IR remote, HA
  // Covers
  cl: "cover", // Curtain
  clkg: "cover", // Curtain switch
  ckmkzq: "cover", // Garage door opener
  mc: "cover", // Door/window controller
  jdcljqr: "cover", // Curtain robot, HA
  // Climate
  wk: "climate", // Thermostat
  wkf: "climate", // Thermostatic radiator valve, HA
  wkcz: "climate", // Two-way temperature and humidity switch, HA
  kt: "climate", // Air conditioner
  ktkzq: "climate", // Air conditioner controller
  ntq: "climate", // HVAC
  qn: "climate", // Heater
  bgl: "climate", // Wall-hung boiler
  rs: "climate", // Water heater
  yb: "climate", // Bathroom heater
  dbl: "climate", // Electric fireplace
  dr: "climate", // Electric blanket
  znrb: "climate", // Pool heat pump, HA
  // Air
  kj: "air", // Air purifier
  xfj: "air", // Ventilation system
  jsq: "air", // Humidifier
  cs: "air", // Dehumidifier
  xxj: "air", // Diffuser
  cwjwq: "air", // Odor eliminator, HA
  // Fans
  fs: "fan", // Fan
  ks: "fan", // Tower fan, HA
  fsd: "fan", // Ceiling fan light
  fskg: "fan", // Fan wall switch, HA
  // Cleaning
  sd: "vacuum", // Robot vacuum
  // Security
  mal: "safety", // Alarm host
  sgbj: "safety", // Siren alarm
  sos: "safety", // Emergency button
  ywbj: "safety", // Smoke alarm
  cobj: "safety", // CO detector
  co2bj: "safety", // CO2 detector
  rqbj: "safety", // Gas alarm
  jwbj: "safety", // Methane detector
  jqbj: "safety", // Formaldehyde detector, HA
  dgnbj: "safety", // Multi-functional alarm
  sj: "safety", // Water leak detector
  sp: "safety", // Smart camera
  dghsxj: "safety", // Low power camera, HA
  // Locks
  ms: "lock", // Residential lock
  jtmspro: "lock", // Residential lock pro
  jtmsbh: "lock", // Smart lock (keep alive)
  gyms: "lock", // Business lock
  hotelms: "lock", // Hotel lock
  ms_category: "lock", // Lock accessories
  videolock: "lock", // Lock with camera
  photolock: "lock", // Audio and video lock
  mk: "lock", // Access control
  bxx: "lock", // Safe box
  // Sensors
  mcs: "sensor", // Contact sensor
  pir: "sensor", // Human motion sensor
  hps: "sensor", // Human presence sensor
  zd: "sensor", // Vibration sensor
  wsdcg: "sensor", // Temperature and humidity sensor
  qxj: "sensor", // Temperature and humidity sensor with probe, HA
  ldcg: "sensor", // Luminance sensor
  ylcg: "sensor", // Pressure sensor
  "pm2.5": "sensor", // PM2.5 detector
  voc: "sensor", // VOC sensor, HA
  hjjcy: "sensor", // Air quality monitor, HA
  szjcy: "sensor", // Water tester, HA
  ywcgq: "sensor", // Tank level sensor, HA
  zwjcy: "sensor", // Soil sensor, HA
  znsb: "sensor", // Smart water meter
  wxkg: "sensor", // Wireless switch: it reports presses, HA
  // Large home appliances
  bx: "appliance", // Refrigerator
  xy: "appliance", // Washing machine
  yg: "appliance", // Bathtub
  lyj: "appliance", // Drying rack
  mjj: "appliance", // Towel rack
  sf: "appliance", // Sofa
  sjz: "appliance", // Electric desk, HA
  ds: "appliance", // TV set
  tyy: "appliance", // Projector
  js: "appliance", // Water purifier
  // Kitchen
  bh: "appliance", // Smart kettle
  tnq: "appliance", // Smart milk kettle
  mb: "appliance", // Bread maker
  kfj: "appliance", // Coffee maker
  nnq: "appliance", // Bottle warmer
  cn: "appliance", // Milk dispenser
  mzj: "appliance", // Sous vide cooker
  mg: "appliance", // Rice cabinet
  dcl: "appliance", // Induction cooker
  kqzg: "appliance", // Air fryer
  znfh: "appliance", // Bento box
  swtz: "appliance", // Cooking thermometer, HA
  // Pets
  cwwsq: "appliance", // Pet feeder
  cwtswsq: "appliance", // Pet treat feeder
  cwwqfsq: "appliance", // Pet ball thrower
  cwysj: "appliance", // Pet fountain
  msp: "appliance", // Cat toilet
  // Garden
  ggq: "appliance", // Irrigator
  sfkzq: "appliance", // Smart water timer, HA
  sz: "appliance", // Smart indoor garden
  hwsb: "appliance", // Outdoor equipment, HA
  // Health and personal
  amy: "appliance", // Massage chair
  liliao: "appliance", // Physiotherapy product
  ts: "appliance", // Smart jump rope
  tzc1: "appliance", // Body fat scale
  sb: "appliance", // Watch/band
  tracker: "appliance", // Tracker
  znyh: "appliance", // Smart pill box
  bzyd: "appliance", // White noise machine, HA
};

/** The kind of card for a category. Any wg* is a gateway, since Tuya has more of them than it lists. */
export function kindOf(category: string): string {
  // Own keys only: "constructor" and the like are object properties, not categories.
  if (Object.prototype.hasOwnProperty.call(CATEGORIES, category)) return CATEGORIES[category];
  return category.startsWith("wg") ? "infrastructure" : "other";
}
