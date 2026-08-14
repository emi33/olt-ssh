-- ============================================================================
-- Registro de clientes/ONUs para carga en OLT DS-P7001 (idOlt = 4)
-- Esquema (CREATE TABLE) + consultas de referencia del flujo de carga.
-- ============================================================================

-- ---------------------------------------------------------------------------
-- 1) ESQUEMA (crear tablas) — ÚNICA parte manual del archivo. Correr UNA vez.
--    Orden importante por las FKs: referencia (planes/gem_config) primero,
--    luego registro_onu, y al final registro_cliente (que apunta a ambas).
--    Los nombres de constraint FK son ÚNICOS por schema: por eso NO se llaman
--    'fk_cliente_onu' (ese ya lo usa eqcliente en read-olt), sino fk_regcliente_*.
--    La config GEM (T-CONT + gem_port + gem_mapping) va UNIFICADA en gem_config.
--
--    Para RE-crear desde cero, descomentá estos DROP (hijas primero):
--    DROP TABLE IF EXISTS olt_test.registro_cliente;
--    DROP TABLE IF EXISTS olt_test.registro_onu;
--    DROP TABLE IF EXISTS olt_test.gem_config;
--    DROP TABLE IF EXISTS olt_test.planes;
-- ---------------------------------------------------------------------------

-- 1.1) Planes de servicio (codigo_trafico = traffic-in/out). Extensible.
CREATE TABLE olt_test.planes (
  id             INT          NOT NULL AUTO_INCREMENT PRIMARY KEY,
  nombre         VARCHAR(50)  NOT NULL,
  velocidad_mb   INT          DEFAULT NULL,
  tipo           ENUM('hogar','pyme','otro') NOT NULL DEFAULT 'hogar',
  codigo_trafico INT          NOT NULL,
  UNIQUE KEY uq_nombre (nombre)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO olt_test.planes (nombre, velocidad_mb, tipo, codigo_trafico) VALUES
  ('100 MB Hogar', 100, 'hogar', 1),
  ('200 MB Hogar', 200, 'hogar', 2),
  ('300 MB Hogar', 300, 'hogar', 3),
  ('100 MB Pyme',  100, 'pyme',  4),
  ('200 MB Pyme',  200, 'pyme',  5),
  ('300 MB Pyme',  300, 'pyme',  6);

-- 1.2) CONFIG GEM (DS-P7001) en UNA sola tabla: unifica T-CONT + GEM PORT + GEM
--      MAPPING (antes 3 tablas). Cada fila = una regla de mapping (una VLAN) con
--      su gem_port y su tcont. Como todo cuelga de T-CONT 1 y cada VLAN mapea a un
--      solo gem_port, alcanza con esta tabla. El gem_port_id es el "gem-id".
CREATE TABLE olt_test.gem_config (
  id_olt          INT NOT NULL,
  tcont_id        INT NOT NULL,
  gem_port_id     INT NOT NULL,               -- = gem-id del service-port
  gem_mapping_id  INT NOT NULL,
  vlan            INT NOT NULL,
  prioridad       INT DEFAULT NULL,
  port            INT DEFAULT NULL,
  PRIMARY KEY (id_olt, gem_port_id, gem_mapping_id),
  UNIQUE KEY uq_olt_vlan (id_olt, vlan),       -- una VLAN -> un solo gem_port
  KEY idx_vlan  (id_olt, vlan),
  KEY idx_tcont (id_olt, tcont_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Datos de las 3 imágenes de la DS-P7001 (tcont_id=1 en todas):
INSERT INTO olt_test.gem_config (id_olt, tcont_id, gem_port_id, gem_mapping_id, vlan) VALUES
  (4, 1, 1, 1, 20), (4, 1, 1, 2, 40), (4, 1, 1, 3, 50), (4, 1, 1, 4, 65), (4, 1, 1, 5, 75), (4, 1, 1, 6, 66),
  (4, 1, 2, 1, 600), (4, 1, 2, 2, 620), (4, 1, 2, 3, 630), (4, 1, 2, 4, 1001),
  (4, 1, 2, 5, 3996), (4, 1, 2, 6, 3991), (4, 1, 2, 7, 3990), (4, 1, 2, 8, 4000),
  (4, 1, 3, 1, 102), (4, 1, 3, 2, 108), (4, 1, 3, 3, 110);

-- 1.5) ONU a registrar (→ ont add). Crear antes que registro_cliente (FK).
CREATE TABLE olt_test.registro_onu (
  id                BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  id_olt            INT          NOT NULL,
  puerto            INT          NOT NULL,
  ont_id            INT          NOT NULL,
  sn                VARCHAR(20)  DEFAULT NULL,
  estado_presencia  VARCHAR(20)  DEFAULT NULL,
  cantidad_macs     INT          NOT NULL DEFAULT 0,
  desc_ont          VARCHAR(100) DEFAULT NULL,
  estado_registro   VARCHAR(60)  NOT NULL DEFAULT 'principal_pendiente',
  listo_para_cargar TINYINT(1)   NOT NULL DEFAULT 0,
  motivo            VARCHAR(255) DEFAULT NULL,
  fecha_alta        TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  fecha_actualizacion TIMESTAMP  NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  fecha_registrada  TIMESTAMP    NULL DEFAULT NULL,
  UNIQUE KEY uq_olt_puerto_ont  (id_olt, puerto, ont_id),
  UNIQUE KEY uq_regonu_olt_sn   (id_olt, sn),
  KEY idx_regonu_estado (id_olt, estado_registro)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 1.6) Cliente / service-port (→ service-port). 0..N por ONU.
CREATE TABLE olt_test.registro_cliente (
  id                BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  id_olt            INT          NOT NULL,
  puerto            INT          NOT NULL,
  ont_id            INT          NOT NULL,
  mac_wan           VARCHAR(17)  NOT NULL,
  pppoe             VARCHAR(100) DEFAULT NULL,             -- NULL si la MAC no tiene cliente en CT (permite varias)
  vlan              INT          NOT NULL DEFAULT 0,     -- parte de uq_olt_mac_vlan: no puede ser NULL
  plan_id           INT          DEFAULT NULL,             -- FK a planes (NULL = plan no reconocido)
  plan              VARCHAR(50)  DEFAULT NULL,             -- texto crudo del plan tal como vino (vacío si no trae)
  gem_id            INT          DEFAULT NULL,             -- derivado de la vlan (gem_mapping)
  idx               INT          DEFAULT NULL,             -- índice de la MAC en la Kingtype (trazabilidad)
  fecha_eq          DATETIME     DEFAULT NULL,             -- última fechaHora de la MAC en eqcliente
  sp_index          INT          DEFAULT NULL,             -- índice del service-port, asignado en vivo
  nro_cliente       INT          DEFAULT NULL,
  ip_cliente        VARCHAR(45)  DEFAULT NULL,
  ip_controlador    VARCHAR(45)  DEFAULT NULL,
  estado_registro   VARCHAR(60)  NOT NULL DEFAULT 'principal_pendiente',
  listo_para_cargar TINYINT(1)   NOT NULL DEFAULT 0,
  motivo            VARCHAR(255) DEFAULT NULL,
  fecha_alta        TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP,
  fecha_actualizacion TIMESTAMP  NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  fecha_creado      TIMESTAMP    NULL DEFAULT NULL,
  -- Un SERVICIO (mac, vlan) por OLT: la misma MAC puede tener varias vlans, cada
  -- una con su idx (ver registro_migraciones_multivlan.sql). No hay unique por
  -- pppoe: un cliente cuya MAC presta dos servicios aparece en las dos filas.
  UNIQUE KEY uq_olt_mac_vlan (id_olt, mac_wan, vlan),
  KEY idx_regcli_onu    (id_olt, puerto, ont_id),
  KEY idx_regcli_estado (id_olt, estado_registro),
  CONSTRAINT fk_regcliente_regonu FOREIGN KEY (id_olt, puerto, ont_id)
      REFERENCES olt_test.registro_onu (id_olt, puerto, ont_id),
  CONSTRAINT fk_regcliente_plan FOREIGN KEY (plan_id)
      REFERENCES olt_test.planes (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- NOTA: eqcliente YA tiene la columna `idx` (ver read-olt/schema.sql). La
-- consulta principal la trae como `indice` y se guarda en registro_cliente.idx.

-- ---------------------------------------------------------------------------
-- 2) CATÁLOGO DE ESTADOS (texto libre; el programa compone <origen>_<caso>)
--    origen: principal | repaso | sobrescrito_repaso
--    listo_para_cargar = 1 SOLO para el caso feliz (1 ONU : 1 MAC : 1 cliente activo).
--
--    listo                              -> 1 onu, 1 mac, cliente activo (OK, cargable)
--    sin_cliente_en_ct                  -> caso 2: 1:1 pero sin cliente en CT / inactivo
--    cliente_inactivo_omitido           -> caso 5: cliente hallado pero activo=0 (se omite)
--    onu_dos_macs_dos_clientes          -> caso 1: 1 ONU con 2 MACs, cada una su cliente
--    onu_multimac_parcial               -> caso 3: varias MACs, algunas con cliente
--    onu_multimac_sin_cliente           -> caso 3: varias MACs, ninguna con cliente
--    onu_sin_mac_placeholder            -> caso 4: ONU sin MAC real (solo 00:00)
--    onu_mixta_real_y_placeholder       -> caso 4: MAC real + índices 00:00 en el mismo puerto
--    vlan_mayor_3000_camara             -> caso 6: CÁMARA. La ONU tiene una o varias MACs
--                                          con vlan>3000 (4000/3996/3991/3990...), sin cliente.
--                                          Es a nivel ONU (prioridad sobre multi-MAC): la ONU
--                                          y TODOS sus clientes quedan con este estado.
--    vlan_mayor_3000_con_cliente        -> caso 6: cámara con al menos una MAC con cliente
--    ausente_sin_mac_patron_no_coincide -> caso 7: mac vieja que NO coincide patrón sn/mac
--    ausente_sin_mac_patron_ok_sin_cliente -> caso 7: patrón ok pero sin cliente en CT
--    puerto_ocupado_mac_vieja_previa    -> caso 8: el puerto/onu_id ya tiene registro cargado
--    vlan_1001_aire                     -> caso 9: vlan 1001 (aire), sin cliente en estos CT
--    mac_sin_onu_en_puerto              -> caso 10: MAC sin ONU registrada en ese puerto
--    encontrado_en_otro_ct              -> (solo repaso) la MAC no tiene cliente en los CT
--                                          configurados (4.5/4.14) pero SÍ aparece en OTRO CT.
--                                          ip_controlador guarda el CT donde se encontró.
--    varios_clientes_en_ct              -> (solo repaso) la MAC matcheó VARIOS clientes en CT
--                                          (uno activo + otros para borrar). Se guarda el de
--                                          ct.fecha más nueva; se marca así (onu y cliente) para
--                                          NO confundir la ONU con un listo para subir.
--    repaso_relleno                     -> (relleno final del repaso) ONU que estaba en `onu`
--                                          pero no había entrado a registro_onu (p. ej.
--                                          ausente_sin_sn sin macs). Se agrega para que
--                                          registro_onu tenga la MISMA cantidad que `onu`.
--    (ausente_sin_sn)                   -> NO se carga en NINGÚN flujo (cargar ni repaso): esas
--                                          ONUs no existen registradas, solo quedan en la BD
--                                          como historial. Tampoco entran en el relleno final.
--
-- NOTA fechas: cada corrida de cargar/repaso SOBREESCRIBE la fila (aunque los datos
-- sean iguales) y bumpea `fecha_actualizacion` (ON DUPLICATE ... = CURRENT_TIMESTAMP),
-- para trazar cuándo fue el último toque. `fecha_alta` queda con el primer alta.
-- ---------------------------------------------------------------------------

-- ===========================================================================
-- REFERENCIA (secciones 3 a 6): estas consultas las EJECUTA EL PROGRAMA
-- (cmd/cargar y cmd/repaso), no se corren a mano. Se documentan acá para
-- entender qué hace cada pasada. Lo ÚNICO manual de este archivo son los
-- CREATE TABLE de la sección 1 (DDL) — la sección 2 es solo el catálogo.
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- 3) CONSULTA PRINCIPAL (parametrizada) — la que hoy trae los clientes activos
--    de los CT concentradores. `?fecha` y la lista de `?ipCT` salen de ENV
--    (PROVISIONING_FECHA_DESDE, PROVISIONING_CONTROLADORES) e idOlt de PROVISIONING_ID_OLT.
--    Trae `eq.idx as indice` (trazabilidad del índice Kingtype).
-- ---------------------------------------------------------------------------
SELECT
    ct.mac              AS mac_wan,
    ct.cliente          AS pppoe,
    ct.ipCliente,
    ct.ipCT             AS ipControlador,
    ct.fecha            AS fechaCT,
    c.nroConexion       AS nroCliente,
    eq.vlan,
    eq.puertoOlt        AS puertoPon,
    eq.identificador    AS onu_id,
    eq.idx              AS indice,          -- <-- índice de la MAC en la Kingtype
    ct.activo           AS esActivo,
    eq.fechaHora        AS fechaEq,
    c.plan,
    onu.sn,
    onu.identificador   AS onu_identificador,
    onu.estadoPresencia,
    onu.cantidadmacs
FROM observacionesct ct
JOIN (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY mac ORDER BY fechaHora DESC) AS rn
    FROM olt_test.eqcliente
    WHERE idOlt = 4                          -- ? PROVISIONING_ID_OLT
) eq ON UPPER(eq.mac) = UPPER(ct.mac) AND eq.rn = 1
LEFT JOIN conexiones c        ON ct.cliente = c.pppoe
LEFT JOIN olt_test.onu onu    ON onu.idOlt = eq.idOlt
                             AND onu.puertoOlt = eq.puertoOlt
                             AND onu.identificador = eq.identificador
                             AND onu.mac = eq.mac
WHERE ct.mac IS NOT NULL AND ct.mac != ''
  AND ct.fecha  >= '2026-07-27 09:00:46'     -- ? PROVISIONING_FECHA_DESDE
  AND ct.activo = 1
  AND ct.ipCT IN ('172.16.4.14','172.16.4.5'); -- ? PROVISIONING_CONTROLADORES (lista dinámica)

-- ---------------------------------------------------------------------------
-- 4) CONSULTAS DE RESPALDO (segunda pasada / repaso). Se corren DESPUÉS de que
--    la consulta principal ya cargó los clientes activos.
-- ---------------------------------------------------------------------------

-- 4a. Todas las ONUs a considerar para el registro (universo de onu de la OLT 4).
SELECT * FROM olt_test.onu WHERE idOlt = 4;

-- 4b. Histórico de MACs: última aparición de cada SERVICIO (mac, vlan) en
--     eqcliente (idOlt 4). Incluye placeholders 00:00 (para el caso 4). Es el
--     universo del repaso. Se parte por (mac, vlan) y no sólo por mac porque la
--     vlan es parte del PK de eqcliente: una MAC con dos vlans son dos servicios
--     distintos, cada uno con su idx.
SELECT *
FROM (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY mac, vlan ORDER BY fechaHora DESC) AS rn
    FROM olt_test.eqcliente
    WHERE idOlt = 4
) t
WHERE rn = 1;

-- 4c. Solo MACs placeholder 00:00 en eqcliente con fecha > corte (caso 4:
--     índices/puertos sin MAC real). Formato-agnóstico (con o sin ':').
SELECT *
FROM olt_test.eqcliente
WHERE idOlt = 4
  AND REPLACE(mac, ':', '') = '000000000000'
  AND fechaHora >= '2026-07-27 09:00:46';     -- ? PROVISIONING_FECHA_DESDE

-- ---------------------------------------------------------------------------
-- 5) CONSULTA DEL REPASO (reconciliación): parte del histórico de MACs (4b),
--    les busca su ONU en onu (idOlt 4, por puerto+identificador, NO por mac)
--    y su cliente en observacionesct (a partir de la MISMA fecha de corte, para
--    no repetir). Devuelve MAC + registro ONU + registro CT en una fila.
-- ---------------------------------------------------------------------------
SELECT
    eq.mac              AS mac_wan,
    eq.puertoOlt        AS puertoPon,
    eq.identificador    AS onu_id,
    eq.idx              AS indice,
    eq.vlan,
    eq.fechaHora        AS fechaEq,
    onu.sn,
    onu.identificador   AS onu_identificador,
    onu.estadoPresencia,
    onu.cantidadmacs,
    ct.cliente          AS pppoe,
    ct.ipCliente,
    ct.ipCT             AS ipControlador,
    ct.fecha            AS fechaCT,
    ct.activo           AS esActivo,
    c.nroConexion       AS nroCliente,
    c.plan
FROM (
    SELECT *, ROW_NUMBER() OVER (PARTITION BY mac ORDER BY fechaHora DESC) AS rn
    FROM olt_test.eqcliente
    WHERE idOlt = 4                          -- ? PROVISIONING_ID_OLT
) eq
LEFT JOIN olt_test.onu onu
       ON onu.idOlt = eq.idOlt
      AND onu.puertoOlt = eq.puertoOlt
      AND onu.identificador = eq.identificador   -- por posición, NO por mac
LEFT JOIN observacionesct ct
       ON UPPER(ct.mac) = UPPER(eq.mac)
      AND ct.activo IN (0,1)
      AND ct.fecha >= '2026-07-27 09:00:46'      -- ? misma fecha que la principal
      AND ct.ipCT IN ('172.16.4.14','172.16.4.5')-- ? PROVISIONING_CONTROLADORES
LEFT JOIN conexiones c ON ct.cliente = c.pppoe
WHERE eq.rn = 1;

-- ---------------------------------------------------------------------------
-- 6) GUARDADO EN LAS TABLAS DE REGISTRO — NO correr a mano.
--    La carga de registros la hace EL PROGRAMA, no este archivo. Cada corrida
--    (cargar y repaso) BORRA lo anterior de esa OLT y escribe lo nuevo dentro de
--    UNA transacción, así no quedan restos de corridas viejas ni las tablas a
--    medio cargar si algo falla. Eso y la cascada ONU→clientes viven en Go:
--        internal/database/database.go : ReemplazarRegistros
--        internal/database/clasificador.go        (1ª pasada, cmd/cargar)
--        internal/database/clasificador_repaso.go (2ª pasada, cmd/repaso)
--    Se ejecutan con:  go run ./cmd/cargar   y luego   go run ./cmd/repaso
--    (esta sección se deja solo como puntero; antes tenía los templates SQL).
-- ---------------------------------------------------------------------------
