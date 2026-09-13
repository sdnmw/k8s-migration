package io.smartx.demo;

import java.net.InetAddress;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.time.Instant;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicBoolean;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.boot.CommandLineRunner;
import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;
import org.springframework.http.MediaType;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.scheduling.annotation.EnableScheduling;
import org.springframework.scheduling.annotation.Scheduled;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@SpringBootApplication
@EnableScheduling
public class MigrationDemoApplication {
  public static void main(String[] args) {
    SpringApplication.run(MigrationDemoApplication.class, args);
  }

  @Bean
  CommandLineRunner initialize(JdbcTemplate jdbc) {
    return args -> {
      jdbc.execute("CREATE TABLE IF NOT EXISTS demo_records (id BIGSERIAL PRIMARY KEY, message VARCHAR(255) UNIQUE NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now())");
      jdbc.update("INSERT INTO demo_records(message) VALUES (?) ON CONFLICT (message) DO NOTHING", "SPRING-NACOS-SOURCE-SEED");
    };
  }
}

@RestController
@RequestMapping("/api")
class DemoController {
  private final JdbcTemplate jdbc;
  private final NacosRegistration nacos;

  DemoController(JdbcTemplate jdbc, NacosRegistration nacos) {
    this.jdbc = jdbc;
    this.nacos = nacos;
  }

  @GetMapping("/records")
  List<Map<String, Object>> records() {
    return jdbc.queryForList("SELECT id, message, created_at FROM demo_records ORDER BY id");
  }

  @PostMapping(value = "/records", consumes = MediaType.APPLICATION_JSON_VALUE)
  Map<String, Object> add(@RequestBody Map<String, String> body) {
    String message = body.getOrDefault("message", "").trim();
    if (message.isEmpty() || message.length() > 255) throw new IllegalArgumentException("message is required");
    jdbc.update("INSERT INTO demo_records(message) VALUES (?) ON CONFLICT (message) DO NOTHING", message);
    return Map.of("stored", message, "at", Instant.now().toString());
  }

  @GetMapping("/health")
  Map<String, Object> health() {
    Integer count = jdbc.queryForObject("SELECT count(*) FROM demo_records", Integer.class);
    return Map.of("application", "nacos-springboot-migration-demo", "databaseRecords", count, "nacosRegistered", nacos.registered());
  }
}

@RestController
class WebController {
  @GetMapping(value = "/", produces = MediaType.TEXT_HTML_VALUE)
  String page() {
    return """
      <!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>Spring Boot + Nacos 迁移演示</title>
      <style>body{font:16px system-ui;max-width:900px;margin:48px auto;color:#172033}h1{color:#1677ff}.card{padding:24px;border:1px solid #dfe5ef;border-radius:12px;box-shadow:0 8px 28px #17203312}pre{background:#f5f7fa;padding:16px;border-radius:8px}</style></head>
      <body><div class="card"><h1>Spring Boot + Nacos + PostgreSQL</h1><p>这是 SKS Migration Center 的 Compose 有状态迁移演示。</p><pre id="health">loading...</pre><pre id="records">loading...</pre></div>
      <script>Promise.all([fetch('/api/health').then(r=>r.json()),fetch('/api/records').then(r=>r.json())]).then(([h,r])=>{health.textContent=JSON.stringify(h,null,2);records.textContent=JSON.stringify(r,null,2)})</script></body></html>
      """;
  }
}

@org.springframework.stereotype.Component
class NacosRegistration {
  private final String server;
  private final AtomicBoolean registered = new AtomicBoolean(false);
  private final HttpClient client = HttpClient.newHttpClient();

  NacosRegistration(@Value("${demo.nacos.server:nacos:8848}") String server) {
    this.server = server;
  }

  @Scheduled(initialDelay = 3000, fixedDelay = 5000)
  void register() {
    try {
      String ip = InetAddress.getLocalHost().getHostAddress();
      String query = "serviceName=" + URLEncoder.encode("sks-springboot-demo", StandardCharsets.UTF_8) + "&ip=" + ip + "&port=8080";
      HttpRequest request = HttpRequest.newBuilder(URI.create("http://" + server + "/nacos/v1/ns/instance?" + query)).POST(HttpRequest.BodyPublishers.noBody()).build();
      registered.set(client.send(request, HttpResponse.BodyHandlers.discarding()).statusCode() == 200);
    } catch (Exception ignored) {
      registered.set(false);
    }
  }

  boolean registered() { return registered.get(); }
}
