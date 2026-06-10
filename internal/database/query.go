package database

// queryComandosRegistro es la consulta que GENERA los comandos de la OLT a partir
// de las tablas onu y eqcliente. Cada fila devuelve dos cadenas:
//
//   - comando1: línea 'service-port ...' (asocia VLAN/servicio a la ONT)
//   - comando2: línea 'ont add ...'      (da de alta la ONT en el puerto)
//
// Diferencias respecto a la versión PHP:
//   - Los placeholders con nombre (:idOlt, :puertoOlt) pasan a '?' posicionales,
//     ya que go-sql-driver/mysql no soporta parámetros con nombre. El orden es
//     (idOlt, puertoOlt).
//   - Se elimina el 'SET @x := 3697' separado: la inicialización del contador ya
//     ocurre de forma inline en el CROSS JOIN (SELECT @x := 3697) AS inicializador,
//     por lo que toda la lógica vive en una única sentencia (segura frente al pool
//     de conexiones de database/sql).
//
// El contador @x numera correlativamente los service-port: empieza en 3697 y el
// primero queda en 3698 (@x := @x + 1).
const queryComandosRegistro = `
	SELECT
		-- Aplicamos el contador al final, garantizando el orden correlativo puro (1, 2, 3...)
		CONCAT(
			'service-port ', (@x := @x + 1),
			' config gpon 1/1/', temporal.puertoOlt,
			' ont ', temporal.identificador,
			' gem-id ', temporal.gem_id_calculado,
			' svlan ', temporal.svlan_calculada,
			' user-vlan ', temporal.svlan_calculada,
			' tag-action transparent'
		) AS comando1,
		temporal.comando2
	FROM (
		-- Subconsulta: agrupamos, filtramos y ordenamos los datos de la ONT primero
		SELECT
			t1.puertoOlt,
			t1.identificador,
			t1.sn,
			CASE COALESCE(t2.vlan, 666)
				WHEN 10   THEN 1
				WHEN 20   THEN 1
				WHEN 21   THEN 1
				WHEN 30   THEN 1
				WHEN 31   THEN 1
				WHEN 50   THEN 1
				WHEN 62   THEN 1
				WHEN 72   THEN 1
				WHEN 102  THEN 3
				WHEN 104  THEN 3
				WHEN 572  THEN 4
				WHEN 600  THEN 2
				WHEN 620  THEN 2
				WHEN 1001 THEN 2
				WHEN 1062 THEN 2
				ELSE 8
			END AS gem_id_calculado,
			COALESCE(
				GROUP_CONCAT(DISTINCT CASE WHEN t2.vlan NOT IN (1, 2, 80) THEN t2.vlan END ORDER BY t2.vlan SEPARATOR ', '),
				'666'
			) AS svlan_calculada,
			CONCAT('ont add ', t1.identificador, ' sn-auth ', CONCAT(LEFT(t1.sn, 4), '-', SUBSTRING(t1.sn, 5)), ' ont-lineprofile-id 1 ont-srvprofile-id 1') AS comando2
		FROM olt.onu t1
		LEFT JOIN olt.eqcliente t2
			ON (t2.idOlt = t1.idOlt OR t2.mac = t1.mac)
			AND t2.puertoOlt = t1.puertoOlt
			AND t2.identificador = t1.identificador
		WHERE t1.idOlt = ?
		AND t1.puertoOlt = ?
		GROUP BY t1.sn, t1.idOlt, t1.puertoOlt, t1.identificador
		ORDER BY t1.idOlt, t1.puertoOlt, t1.identificador
	) AS temporal
	CROSS JOIN (SELECT @x := 3697) AS inicializador;
`
